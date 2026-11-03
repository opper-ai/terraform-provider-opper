// Package client implements the public Opper Management API HTTP contract.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxResponseBytes = 1024 * 1024

// Client authenticates directly to Opper's Management API. Requests have a
// bounded timeout and mutations are not automatically retried: project creates
// do not offer idempotency keys, so a retry could create an unmanaged object.
type Client struct {
	baseURL    string
	token      string
	userAgent  string
	httpClient *http.Client
}

// Project is the public /management/v1/projects response object.
type Project struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// APIError deliberately excludes response bodies: an upstream error can echo
// credentials or contain other sensitive data. StatusCode lets callers handle
// missing resources without treating authorization or server failures as drift.
type APIError struct{ StatusCode int }

func (e *APIError) Error() string {
	return fmt.Sprintf("Opper Management API returned HTTP %d (%s)", e.StatusCode, http.StatusText(e.StatusCode))
}

// IsNotFound reports whether an API response was HTTP 404, including wrapped
// errors. Transport failures and other API errors must not remove Terraform state.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// requestError preserves context/transport error identity for errors.Is without
// printing URLs, proxy configuration, or arbitrary transport error text.
type requestError struct{ cause error }

func (e *requestError) Error() string {
	if errors.Is(e.cause, context.Canceled) {
		return "request to Opper Management API was canceled"
	}
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "request to Opper Management API timed out"
	}
	return "request to Opper Management API failed; check connectivity and TLS configuration"
}
func (e *requestError) Unwrap() error { return e.cause }

// New constructs a client for an origin URL, e.g. https://api.opper.ai. HTTP is
// allowed only on loopback for local development. Credentials cannot be placed
// in the URL, and redirects are never followed.
func New(baseURL, token, version string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(baseURL, "#") || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("base_url must be an HTTPS origin without credentials, path, query, or fragment; HTTP is allowed only for loopback")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback)) {
		return nil, errors.New("base_url must use HTTPS; HTTP is allowed only for loopback")
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("a non-empty management API key is required")
	}
	if strings.ContainsAny(version, "\r\n") {
		return nil, errors.New("invalid provider version")
	}
	return &Client{
		baseURL: strings.TrimSuffix(parsed.String(), "/"), token: token, userAgent: "terraform-provider-opper/" + version,
		httpClient: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *Client) CreateProject(ctx context.Context, name, description string) (Project, error) {
	return c.project(ctx, http.MethodPost, "/management/v1/projects", map[string]string{"name": name, "description": description})
}

func (c *Client) GetProject(ctx context.Context, id string) (Project, error) {
	path, err := projectPath(id)
	if err != nil {
		return Project{}, err
	}
	p, err := c.project(ctx, http.MethodGet, path, nil)
	if err == nil && p.UUID != id {
		return Project{}, errors.New("Opper Management API returned a different project UUID")
	}
	return p, err
}

// UpdateProject sends only changed attributes. A nil pointer omits a field;
// a pointer to an empty description explicitly clears its previous value.
// Sending an unchanged name would unnecessarily invoke the API's rename guard
// on protected projects, so callers should omit unchanged attributes.
func (c *Client) UpdateProject(ctx context.Context, id string, name, description *string) (Project, error) {
	path, err := projectPath(id)
	if err != nil {
		return Project{}, err
	}
	body := make(map[string]string)
	if name != nil {
		body["name"] = *name
	}
	if description != nil {
		body["description"] = *description
	}
	if len(body) == 0 {
		return Project{}, errors.New("project update requires at least one changed field")
	}
	p, err := c.project(ctx, http.MethodPatch, path, body)
	if err == nil && p.UUID != id {
		return Project{}, errors.New("Opper Management API returned a different project UUID")
	}
	return p, err
}

func (c *Client) DeleteProject(ctx context.Context, id string) error {
	path, err := projectPath(id)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodDelete, path, nil)
	return err
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func projectPath(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", errors.New("invalid project UUID")
	}
	return "/management/v1/projects/" + id, nil
}

func (c *Client) project(ctx context.Context, method, path string, body any) (Project, error) {
	data, err := c.do(ctx, method, path, body)
	if err != nil {
		return Project{}, err
	}
	var envelope struct {
		Data *Project `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil || envelope.Data.UUID == "" || envelope.Data.Name == "" {
		return Project{}, errors.New("Opper Management API returned an invalid project response")
	}
	return *envelope.Data, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var requestBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("could not encode Opper Management API request")
		}
		requestBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return nil, errors.New("could not construct Opper Management API request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &requestError{cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode}
	}
	// Delete has no resource envelope; tolerate both 200 and 204 responses.
	if method == http.MethodDelete {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &requestError{cause: err}
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("Opper Management API response exceeded the size limit")
	}
	return data, nil
}
