package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectsCRUD(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing auth")
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "0.1.0") {
			t.Error("missing version")
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("missing content type")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["name"] != "test" {
				t.Errorf("name=%v", body["name"])
			}
			expectedDescription := "description"
			if r.Method == http.MethodPatch {
				expectedDescription = ""
			}
			if body["description"] != expectedDescription {
				t.Errorf("description=%v", body["description"])
			}
		}
		expectedPath := "/management/v1/projects/id"
		if r.Method == http.MethodPost {
			expectedPath = "/management/v1/projects"
			w.WriteHeader(http.StatusCreated)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Fprint(w, `{"meta":{},"data":{"uuid":"id","name":"test","description":"description","created_at":"2026-11-03T00:00:00Z"}}`)
	}))
	defer server.Close()
	c, err := New(server.URL, "test-secret", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateProject(context.Background(), "test", "description")
	if err != nil {
		t.Fatal(err)
	}
	if created.UUID != "id" || created.Name != "test" || created.CreatedAt == "" {
		t.Fatalf("project=%+v", created)
	}
	if _, err = c.GetProject(context.Background(), "id"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateProject(context.Background(), "id", stringPtr("test"), stringPtr("")); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteProject(context.Background(), "id"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(methods) != "[POST GET PATCH DELETE]" {
		t.Fatal(methods)
	}
}

func TestAPIError(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var count int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				w.WriteHeader(code)
				fmt.Fprint(w, `{"error":"untrusted-secret test-secret","required_scope":"projects:read"}`)
			}))
			defer server.Close()
			c, _ := New(server.URL, "test-secret", "test")
			_, err := c.CreateProject(context.Background(), "test", "")
			if err == nil {
				t.Fatal("expected API error")
			}
			if !strings.Contains(err.Error(), fmt.Sprint(code)) {
				t.Fatal(err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("raw response leaked: %v", err)
			}
			if IsNotFound(err) != (code == 404) {
				t.Fatalf("not found=%v for %d", IsNotFound(err), code)
			}
			if count != 1 {
				t.Fatalf("unexpected retry: %d", count)
			}
		})
	}
}

func TestInvalidProjectResponse(t *testing.T) {
	for _, body := range []string{``, `{`, `null`, `{}`, `{"data":null}`, `{"data":{}}`, `{"data":{"uuid":"id"}}`, `{"data":{"uuid":"id","name":"test"}} trailing`, `{"data":{"uuid":"id","name":"test"}} {}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New(server.URL, "test-secret", "test")
			if _, err := c.GetProject(context.Background(), "id"); err == nil {
				t.Fatal("accepted invalid project envelope")
			}
		})
	}
}

func TestBaseURLValidation(t *testing.T) {
	for _, baseURL := range []string{"https://api.opper.ai", "https://api.opper.ai/", "https://custom.example:8443", "http://127.0.0.1:1234", "http://localhost:1234", "http://[::1]:1234"} {
		if _, err := New(baseURL, "secret", "test"); err != nil {
			t.Errorf("%q: %v", baseURL, err)
		}
	}
	for _, baseURL := range []string{"", "api.opper.ai", "http://api.opper.ai", "ftp://api.opper.ai", "https://user:secret@api.opper.ai", "https://api.opper.ai/v3", "https://api.opper.ai/?secret=value", "https://api.opper.ai/#fragment", "https:///", "https://api.opper.ai/../", "https://api.opper.ai/?", "https://api.opper.ai/#"} {
		if _, err := New(baseURL, "secret", "test"); err == nil {
			t.Errorf("accepted %q", baseURL)
		}
	}
	if _, err := New("https://api.opper.ai", "", "test"); err == nil {
		t.Error("accepted empty token")
	}
	if _, err := New("https://api.opper.ai", "   ", "test"); err == nil {
		t.Error("accepted blank token")
	}
}

func TestRejectRedirects(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetRequests.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	_, err := c.GetProject(context.Background(), "id")
	if err == nil {
		t.Fatal("accepted redirect")
	}
	if targetRequests.Load() != 0 {
		t.Fatal("followed redirect")
	}
}

func TestRequestCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.GetProject(ctx, "id")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestInvalidProjectID(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	for _, id := range []string{"", ".", "..", "x/y", "x?y", "x#y", "%2f", "a b"} {
		if _, err := c.GetProject(context.Background(), id); err == nil {
			t.Errorf("accepted id %q", id)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid ids reached API")
	}
}

func TestResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", maxResponseBytes+1))
	}))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	if _, err := c.GetProject(context.Background(), "id"); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportErrorRedaction(t *testing.T) {
	c, _ := New("https://api.opper.ai", "secret", "test")
	sensitiveError := errors.New("proxy https://user:password@example.com failed with token secret")
	c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, sensitiveError })
	_, err := c.GetProject(context.Background(), "id")
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("transport error not redacted: %v", err)
	}
	if !errors.Is(err, sensitiveError) {
		t.Fatal("lost error identity")
	}
}

func TestResponseIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"uuid":"wrong-id","name":"test"}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	if _, err := c.GetProject(context.Background(), "expected-id"); err == nil {
		t.Fatal("accepted wrong resource")
	}
	if _, err := c.UpdateProject(context.Background(), "expected-id", stringPtr("test"), stringPtr("")); err == nil {
		t.Fatal("accepted wrong resource")
	}
}

func TestDeleteError(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer server.Close()
			c, _ := New(server.URL, "secret", "test")
			err := c.DeleteProject(context.Background(), "id")
			if err == nil || IsNotFound(err) != (code == http.StatusNotFound) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if IsNotFound(nil) || IsNotFound(errors.New("404")) {
		t.Fatal("wrong error type treated as not found")
	}
	if !IsNotFound(fmt.Errorf("wrapped: %w", &APIError{StatusCode: 404})) {
		t.Fatal("wrapped API error ignored")
	}
}

func stringPtr(value string) *string { return &value }

func TestUpdateProjectPartialFields(t *testing.T) {
	tests := []struct {
		name        string
		projectName *string
		description *string
		expected    map[string]string
	}{
		{name: "name only", projectName: stringPtr("renamed"), expected: map[string]string{"name": "renamed"}},
		{name: "description only", description: stringPtr("changed"), expected: map[string]string{"description": "changed"}},
		{name: "clear description", description: stringPtr(""), expected: map[string]string{"description": ""}},
		{name: "both", projectName: stringPtr("renamed"), description: stringPtr("changed"), expected: map[string]string{"name": "renamed", "description": "changed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch {
					t.Errorf("method=%s", r.Method)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != len(test.expected) {
					t.Errorf("unexpected fields: %#v", body)
				}
				for key, want := range test.expected {
					if got, ok := body[key]; !ok || got != want {
						t.Errorf("%s=%q present=%v want=%q", key, got, ok, want)
					}
				}
				fmt.Fprint(w, `{"data":{"uuid":"id","name":"test","description":"changed"}}`)
			}))
			defer server.Close()
			c, _ := New(server.URL, "secret", "test")
			if _, err := c.UpdateProject(context.Background(), "id", test.projectName, test.description); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpdateProjectNoFields(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	if _, err := c.UpdateProject(context.Background(), "id", nil, nil); err == nil {
		t.Fatal("accepted empty update")
	}
	if requests.Load() != 0 {
		t.Fatal("empty update reached API")
	}
}
