package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCreateProjectWithAccessSingleAtomicRequest(t *testing.T) {
	input := InitialProjectAccess{Mode: "restricted", RoleKeys: []string{"viewer", "admin", "viewer"}, UserUUIDs: []string{projectAccessTestUser, projectAccessTestUser}}
	want := Project{UUID: projectAccessTestProject, Name: "private", Description: "initial policy", CreatedAt: "2026-11-13T00:00:00Z"}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/management/v1/projects/with-access" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("User-Agent") != "terraform-provider-opper/test" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			t.Error("creation omitted standard authentication or JSON headers")
		}
		raw, _ := io.ReadAll(r.Body)
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Error(err)
		}
		expected := map[string]any{"name": "private", "description": "initial policy", "access": map[string]any{"mode": "restricted", "role_keys": []any{"admin", "viewer"}, "user_uuids": []any{projectAccessTestUser}}}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("atomic create payload=%s", raw)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": want})
	}))
	defer server.Close()
	c, _ := New(server.URL, "test-key", "test")
	got, err := c.CreateProjectWithAccess(context.Background(), "private", "initial policy", input)
	if err != nil || got != want || calls != 1 {
		t.Fatalf("creation=%+v err=%v calls=%d", got, err, calls)
	}
	if !reflect.DeepEqual(input.RoleKeys, []string{"viewer", "admin", "viewer"}) || len(input.UserUUIDs) != 2 {
		t.Fatal("creation mutated caller-owned recipients")
	}
}

func TestCreateProjectWithAccessExplicitEmptyGrants(t *testing.T) {
	for _, mode := range []string{"organization", "restricted"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), `"role_keys":[]`) || !strings.Contains(string(raw), `"user_uuids":[]`) || !strings.Contains(string(raw), `"description":""`) {
					t.Errorf("missing explicit empty fields: %s", raw)
				}
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"name":"empty"}}`, projectAccessTestProject)
			}))
			defer server.Close()
			c, _ := New(server.URL, "test-key", "test")
			if _, err := c.CreateProjectWithAccess(context.Background(), "empty", "", InitialProjectAccess{Mode: mode}); err != nil || calls != 1 {
				t.Fatalf("creation err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCreateProjectWithAccessChecksWholeBodySize(t *testing.T) {
	access := InitialProjectAccess{Mode: "restricted", RoleKeys: []string{}, UserUUIDs: []string{}}
	base, _ := json.Marshal(map[string]any{"name": "private", "description": "", "access": access})
	for _, size := range []int{maxProjectAccessInputBytes, maxProjectAccessInputBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			calls := 0
			c, _ := New("https://api.opper.ai", "test-key", "test")
			c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if len(raw) != size {
					t.Errorf("request bytes=%d, want %d", len(raw), size)
				}
				return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"data":{"uuid":"created","name":"private"}}`)), Header: make(http.Header)}, nil
			})
			_, err := c.CreateProjectWithAccess(context.Background(), "private", strings.Repeat("x", size-len(base)), access)
			if size == maxProjectAccessInputBytes {
				if err != nil || calls != 1 {
					t.Fatalf("valid boundary rejected: err=%v calls=%d", err, calls)
				}
			} else if !IsLocalValidationError(err) || calls != 0 {
				t.Fatalf("oversized body was not rejected before mutation: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCreateProjectWithAccessInvalidIntentNeverRequests(t *testing.T) {
	calls := 0
	c, _ := New("https://api.opper.ai", "test-key", "test")
	c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected mutation")
	})
	tooManyRoles, tooManyUsers := make([]string, 501), make([]string, 501)
	for i := range tooManyRoles {
		tooManyRoles[i], tooManyUsers[i] = "viewer", projectAccessTestUser
	}
	inputs := []InitialProjectAccess{
		{}, {Mode: "Restricted"},
		{Mode: "organization", RoleKeys: []string{"viewer"}},
		{Mode: "organization", UserUUIDs: []string{projectAccessTestUser}},
		{Mode: "restricted", RoleKeys: []string{"owner"}},
		{Mode: "restricted", RoleKeys: []string{"custom_ABCDEF0123456789abcdef0123456789"}},
		{Mode: "restricted", UserUUIDs: []string{"00000000-0000-0000-0000-000000000000"}},
		{Mode: "restricted", UserUUIDs: []string{"ABCDEF01-3333-4333-8333-333333333333"}},
		{Mode: "restricted", UserUUIDs: []string{"../users"}},
		{Mode: "restricted", RoleKeys: tooManyRoles},
		{Mode: "restricted", UserUUIDs: tooManyUsers},
	}
	for i, input := range inputs {
		if _, err := c.CreateProjectWithAccess(context.Background(), "private", "", input); !IsLocalValidationError(err) {
			t.Errorf("case %d err=%v, wanted pre-request validation", i, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid input made %d requests", calls)
	}
}

func TestCreateProjectWithAccessFailuresNeverFallback(t *testing.T) {
	for _, code := range []int{401, 403, 404, 409, 429, 500, 501, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/management/v1/projects/with-access" {
					t.Error("failure triggered a non-atomic fallback")
				}
				w.WriteHeader(code)
				_, _ = io.WriteString(w, `{"error":"private-response-canary"}`)
			}))
			defer server.Close()
			c, _ := New(server.URL, "test-key", "test")
			_, err := c.CreateProjectWithAccess(context.Background(), "private", "", InitialProjectAccess{Mode: "restricted"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != code || calls != 1 || strings.Contains(err.Error(), "private-response-canary") {
				t.Fatalf("failure err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCreateProjectWithAccessOlderBackendCannotCreateUnrestrictedProject(t *testing.T) {
	var calls, unrestrictedCreates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodPost && r.URL.Path == "/management/v1/projects" {
			// The legacy endpoint accepts unknown fields, ignores access, and
			// creates an Organization project even when Restricted was requested.
			unrestrictedCreates++
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"data":{"uuid":"unsafe","name":"private"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c, _ := New(server.URL, "test-key", "test")
	_, err := c.CreateProjectWithAccess(context.Background(), "private", "", InitialProjectAccess{Mode: "restricted"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound || calls != 1 || unrestrictedCreates != 0 {
		t.Fatalf("older backend must fail closed: err=%v calls=%d unrestricted creates=%d", err, calls, unrestrictedCreates)
	}
}

func TestCreateProjectWithAccessUncertainResponseNeverRetries(t *testing.T) {
	for _, transportFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("transport=%t", transportFailure), func(t *testing.T) {
			calls := 0
			sensitive := errors.New("https://user:password@example.com private-transport-canary")
			c, _ := New("https://api.opper.ai", "test-key", "test")
			c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if transportFailure {
					return nil, sensitive
				}
				return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"data":null,"private":"private-response-canary"}`)), Header: make(http.Header)}, nil
			})
			_, err := c.CreateProjectWithAccess(context.Background(), "private", "", InitialProjectAccess{Mode: "restricted"})
			if err == nil || IsLocalValidationError(err) || calls != 1 || strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "password") {
				t.Fatalf("uncertain outcome err=%v calls=%d", err, calls)
			}
			if transportFailure && !errors.Is(err, sensitive) {
				t.Fatal("lost transport error identity")
			}
		})
	}
}
