package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProjectWebhookReadAndUpdateContract(t *testing.T) {
	const projectID = "11111111-1111-4111-8111-111111111111"
	const endpoint = "/management/v1/projects/" + projectID + "/webhook"
	const configuredURL = "https://events.example.test/opper?token=private-callback-token"
	const populated = `{"data":{"url":"https://events.example.test/opper?token=private-callback-token","url_updated_at":"2026-11-04T10:00:00Z","secret_created_at":"2026-11-03T09:00:00Z","previous_secret_valid_until":"2026-11-04T10:05:00Z"}}`
	const cleared = `{"data":{"url":null,"url_updated_at":"2026-11-04T10:01:00Z","secret_created_at":null,"previous_secret_valid_until":null}}`
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path != endpoint {
			t.Errorf("unexpected webhook surface %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer management-secret" || r.Header.Get("Accept") != "application/json" {
			t.Error("missing request headers")
		}
		switch r.Method {
		case http.MethodGet:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if len(body) != 0 {
				t.Error("webhook read included a mutation body")
			}
			fmt.Fprint(w, populated)
		case http.MethodPatch:
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("missing JSON content type")
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			raw, ok := body["url"]
			if !ok || len(body) != 1 {
				t.Error("PATCH must contain exactly the url field")
			}
			if string(raw) == "null" {
				fmt.Fprint(w, cleared)
				return
			}
			var value string
			if json.Unmarshal(raw, &value) != nil || value != configuredURL {
				t.Error("wrong configured URL")
			}
			fmt.Fprint(w, populated)
		default:
			t.Errorf("unexpected request method %q", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	c, err := New(server.URL, "management-secret", "test")
	if err != nil {
		t.Fatal(err)
	}
	overview, err := c.GetProjectWebhook(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.URL == nil || *overview.URL != configuredURL || overview.URLUpdatedAt == nil || *overview.URLUpdatedAt != "2026-11-04T10:00:00Z" || overview.SecretCreatedAt == nil || *overview.SecretCreatedAt != "2026-11-03T09:00:00Z" || overview.PreviousSecretValidUntil == nil || *overview.PreviousSecretValidUntil != "2026-11-04T10:05:00Z" {
		t.Fatal("webhook overview lost metadata")
	}
	value := configuredURL
	updated, err := c.UpdateProjectWebhook(context.Background(), projectID, &value)
	if err != nil || updated.URL == nil || *updated.URL != configuredURL {
		t.Fatal("webhook URL update failed")
	}
	removed, err := c.UpdateProjectWebhook(context.Background(), projectID, nil)
	if err != nil || removed.URL != nil || removed.URLUpdatedAt == nil || *removed.URLUpdatedAt != "2026-11-04T10:01:00Z" || removed.SecretCreatedAt != nil || removed.PreviousSecretValidUntil != nil {
		t.Fatal("explicit null did not clear URL or preserve nullable metadata")
	}
	if fmt.Sprint(calls) != "[GET "+endpoint+" PATCH "+endpoint+" PATCH "+endpoint+"]" {
		t.Fatal("webhook client accessed an unexpected endpoint or retried a mutation")
	}
}

func TestProjectWebhookEmptyOverview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"meta":{},"data":{"url":null,"url_updated_at":null,"secret_created_at":null,"previous_secret_valid_until":null}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	overview, err := c.GetProjectWebhook(context.Background(), "project-id")
	if err != nil || overview.URL != nil || overview.URLUpdatedAt != nil || overview.SecretCreatedAt != nil || overview.PreviousSecretValidUntil != nil {
		t.Fatal("empty webhook overview was not represented as nullable metadata")
	}
}

func TestProjectWebhookInvalidResponse(t *testing.T) {
	for _, body := range []string{``, `{`, `null`, `{}`, `{"data":null}`, `{"data":[]}`, `{"data":{}}`, `{"data":{"url_updated_at":null}}`, `{"data":{"url":42}}`, `{"data":{"url":null,"secret_created_at":42}}`, `{"data":{"url":"https://events.example.test/private-callback-token"}} trailing`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			_, getErr := c.GetProjectWebhook(context.Background(), "project-id")
			_, patchErr := c.UpdateProjectWebhook(context.Background(), "project-id", nil)
			for _, err := range []error{getErr, patchErr} {
				if err == nil || strings.Contains(err.Error(), "private-callback-token") {
					t.Fatal("malformed webhook response was accepted or echoed")
				}
			}
		})
	}
}

func TestProjectWebhookAPIErrorsAreRedactedAndNotRetried(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 429, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(code)
				fmt.Fprint(w, `{"error":"management-secret https://events.example.test/private-callback-token"}`)
			}))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			_, getErr := c.GetProjectWebhook(context.Background(), "project-id")
			_, patchErr := c.UpdateProjectWebhook(context.Background(), "project-id", nil)
			for _, err := range []error{getErr, patchErr} {
				if err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-callback-token") || IsNotFound(err) != (code == 404) {
					t.Fatal("wrong HTTP error classification or untrusted error body exposed")
				}
			}
			if requests.Load() != 2 {
				t.Fatal("webhook requests were automatically retried")
			}
		})
	}
}

func TestProjectWebhookRejectsInvalidProjectPaths(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	for _, projectID := range []string{"", "..", "a/b", "a?b", "a#b", "%2f"} {
		if _, err := c.GetProjectWebhook(context.Background(), projectID); err == nil {
			t.Error("invalid project read accepted")
		}
		if _, err := c.UpdateProjectWebhook(context.Background(), projectID, nil); err == nil {
			t.Error("invalid project update accepted")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid project path reached the API")
	}
}
