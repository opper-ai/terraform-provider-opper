package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const modelListTestOrgUUID = "11111111-1111-4111-8111-111111111111"

func TestOrganizationModelListClient(t *testing.T) {
	calls := 0
	current := "all"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/management/v1/organization/model-list" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		switch r.Method {
		case http.MethodGet:
		case http.MethodPut:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if len(body) != 1 || body["compat_model_list"] == "" {
				t.Errorf("unexpected PUT fields: %v", body)
			}
			current = body["compat_model_list"]
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(405)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": map[string]string{"organization_uuid": modelListTestOrgUUID, "compat_model_list": current}})
	}))
	defer server.Close()
	c, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetOrganizationModelList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OrganizationUUID != modelListTestOrgUUID || got.CompatModelList != "all" {
		t.Fatalf("unexpected overview: %+v", got)
	}
	for _, value := range []string{"simplified", "recommended", "all"} {
		got, err = c.SetOrganizationModelList(context.Background(), value)
		if err != nil {
			t.Fatal(err)
		}
		if got.OrganizationUUID != modelListTestOrgUUID || got.CompatModelList != value {
			t.Fatalf("unexpected updated overview: %+v", got)
		}
	}
	if calls != 4 {
		t.Fatalf("expected four single requests, got %d", calls)
	}
}

func TestOrganizationModelListClientRejectsInvalidResponse(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"data":null}`, `{"data":{"compat_model_list":"all"}}`,
		`{"data":{"organization_uuid":"not-a-uuid","compat_model_list":"all"}}`,
		`{"data":{"organization_uuid":"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA","compat_model_list":"all"}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111"}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":"unknown"}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":null}}`,
		`{"data":`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			c, err := New(server.URL, "test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.GetOrganizationModelList(context.Background()); err == nil {
				t.Fatal("invalid GET response accepted")
			}
			if _, err = c.SetOrganizationModelList(context.Background(), "all"); err == nil {
				t.Fatal("invalid PUT response accepted")
			}
			if calls != 2 {
				t.Fatalf("invalid response triggered retry/fallback: %d calls", calls)
			}
		})
	}
}

func TestOrganizationModelListClientInvalidInputAndHTTPError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"private details must not be exposed"}`))
	}))
	defer server.Close()
	c, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "ALL", " all", "unknown"} {
		if _, err = c.SetOrganizationModelList(context.Background(), value); err == nil {
			t.Errorf("invalid input %q accepted", value)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached HTTP")
	}
	if _, err = c.GetOrganizationModelList(context.Background()); err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected sanitized GET error: %v", err)
	}
	if _, err = c.SetOrganizationModelList(context.Background(), "all"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("unexpected PUT error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("HTTP error triggered retry/fallback: %d calls", calls)
	}
}
