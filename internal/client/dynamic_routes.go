package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

// DynamicRouteDetail is the management API's route/draft envelope. The active
// version summary is observational; the separate deployment resource reads
// the exact immutable version before asserting ownership.
type DynamicRouteDetail struct {
	Route struct {
		UUID            string `json:"uuid"`
		Name            string `json:"name"`
		DisplayName     string `json:"display_name"`
		Description     string `json:"description"`
		InvocationModel string `json:"invocation_model"`
		CreatedAt       string `json:"created_at"`
	} `json:"route"`
	Draft struct {
		Graph    json.RawMessage `json:"graph"`
		Revision int64           `json:"revision"`
	} `json:"draft"`
	ActiveVersion *struct {
		UUID          string `json:"uuid"`
		VersionNumber int64  `json:"version_number"`
	} `json:"active_version,omitempty"`
}

var dynamicRouteNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func dynamicRoutePath(name string) (string, error) {
	if !dynamicRouteNamePattern.MatchString(name) {
		return "", errors.New("invalid dynamic route name")
	}
	return "/management/v1/dynamic-routes/" + name, nil
}

func (c *Client) dynamicRoute(ctx context.Context, method, path, expectedName string, body any) (DynamicRouteDetail, error) {
	data, err := c.do(ctx, method, path, body)
	if err != nil {
		return DynamicRouteDetail{}, err
	}
	var envelope struct {
		Data *DynamicRouteDetail `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil || envelope.Data.Route.UUID == "" || envelope.Data.Route.Name != expectedName || envelope.Data.Draft.Revision < 1 || len(envelope.Data.Draft.Graph) == 0 || string(envelope.Data.Draft.Graph) == "null" {
		return DynamicRouteDetail{}, errors.New("Opper Management API returned an invalid dynamic route response")
	}
	return *envelope.Data, nil
}

func (c *Client) CreateDynamicRoute(ctx context.Context, name, displayName, description string, graph json.RawMessage) (DynamicRouteDetail, error) {
	if _, err := dynamicRoutePath(name); err != nil {
		return DynamicRouteDetail{}, err
	}
	return c.dynamicRoute(ctx, http.MethodPost, "/management/v1/dynamic-routes", name, map[string]any{
		"name": name, "display_name": displayName, "description": description, "graph": graph, "deploy": false,
	})
}

func (c *Client) GetDynamicRoute(ctx context.Context, name string) (DynamicRouteDetail, error) {
	path, err := dynamicRoutePath(name)
	if err != nil {
		return DynamicRouteDetail{}, err
	}
	return c.dynamicRoute(ctx, http.MethodGet, path, name, nil)
}

func (c *Client) UpdateDynamicRouteDisplayName(ctx context.Context, name, displayName string) (DynamicRouteDetail, error) {
	path, err := dynamicRoutePath(name)
	if err != nil {
		return DynamicRouteDetail{}, err
	}
	return c.dynamicRoute(ctx, http.MethodPatch, path, name, map[string]string{"display_name": displayName})
}

// UpdateDynamicRouteDraft omits layout so the API preserves editor positions.
// Nil fields are unchanged; a non-nil empty description explicitly clears it.
func (c *Client) UpdateDynamicRouteDraft(ctx context.Context, name string, description *string, graph json.RawMessage) (DynamicRouteDetail, error) {
	path, err := dynamicRoutePath(name)
	if err != nil {
		return DynamicRouteDetail{}, err
	}
	body := make(map[string]any)
	if description != nil {
		body["description"] = *description
	}
	if graph != nil {
		body["graph"] = graph
	}
	if len(body) == 0 {
		return DynamicRouteDetail{}, errors.New("dynamic route draft update requires at least one changed field")
	}
	return c.dynamicRoute(ctx, http.MethodPut, path+"/draft", name, body)
}

func (c *Client) DeleteDynamicRoute(ctx context.Context, name string) error {
	path, err := dynamicRoutePath(name)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodDelete, path, nil)
	return err
}
