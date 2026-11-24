package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const modelListTestOrgUUID = "11111111-1111-4111-8111-111111111111"

func modelListString(v string) *string { return &v }
func modelListBool(v bool) *bool       { return &v }

func TestOrganizationModelListClient(t *testing.T) {
	calls := 0
	current, routes := "all", true
	var bodies []map[string]any
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
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			bodies = append(bodies, body)
			for key, value := range body {
				switch key {
				case "compat_model_list":
					current = value.(string)
				case "compat_model_list_dynamic_routes":
					routes = value.(bool)
				default:
					t.Errorf("unexpected PUT field %q", key)
				}
			}
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(405)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": map[string]any{"organization_uuid": modelListTestOrgUUID, "compat_model_list": current, "compat_model_list_dynamic_routes": routes}})
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
	if got.OrganizationUUID != modelListTestOrgUUID || got.CompatModelList != "all" || !got.DynamicRoutes {
		t.Fatalf("unexpected overview: %+v", got)
	}
	for _, value := range []string{"simplified", "recommended", "none", "all"} {
		got, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{CompatModelList: modelListString(value)})
		if err != nil {
			t.Fatal(err)
		}
		if got.OrganizationUUID != modelListTestOrgUUID || got.CompatModelList != value || !got.DynamicRoutes {
			t.Fatalf("unexpected updated overview: %+v", got)
		}
	}
	got, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{DynamicRoutes: modelListBool(false)})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompatModelList != "all" || got.DynamicRoutes {
		t.Fatalf("routes-only PUT changed the wrong field: %+v", got)
	}
	got, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{CompatModelList: modelListString("none"), DynamicRoutes: modelListBool(true)})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompatModelList != "none" || !got.DynamicRoutes {
		t.Fatalf("unexpected combined PUT result: %+v", got)
	}
	if calls != 7 {
		t.Fatalf("expected seven single requests, got %d", calls)
	}
	// A field the caller leaves out is left out of the body, so the API keeps it.
	wantFields := [][]string{{"compat_model_list"}, {"compat_model_list"}, {"compat_model_list"}, {"compat_model_list"}, {"compat_model_list_dynamic_routes"}, {"compat_model_list", "compat_model_list_dynamic_routes"}}
	for i, want := range wantFields {
		if len(bodies[i]) != len(want) {
			t.Fatalf("PUT %d body %v, want fields %v", i, bodies[i], want)
		}
		for _, key := range want {
			if _, ok := bodies[i][key]; !ok {
				t.Fatalf("PUT %d body %v lacks %s", i, bodies[i], key)
			}
		}
	}
}

func TestOrganizationModelListClientAcceptsNone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta":{},"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":"none","compat_model_list_dynamic_routes":true}}`))
	}))
	defer server.Close()
	c, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetOrganizationModelList(context.Background())
	if err != nil || got.CompatModelList != "none" || !got.DynamicRoutes {
		t.Fatalf("organization set to none was not readable: %+v %v", got, err)
	}
}

func TestOrganizationModelListClientRejectsInvalidResponse(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"data":null}`, `{"data":{"compat_model_list":"all","compat_model_list_dynamic_routes":true}}`,
		`{"data":{"organization_uuid":"not-a-uuid","compat_model_list":"all","compat_model_list_dynamic_routes":true}}`,
		`{"data":{"organization_uuid":"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA","compat_model_list":"all","compat_model_list_dynamic_routes":true}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list_dynamic_routes":true}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":"unknown","compat_model_list_dynamic_routes":true}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":null,"compat_model_list_dynamic_routes":true}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":"all"}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":"all","compat_model_list_dynamic_routes":null}}`,
		`{"data":{"organization_uuid":"11111111-1111-4111-8111-111111111111","compat_model_list":"all","compat_model_list_dynamic_routes":"true"}}`,
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
			if _, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{CompatModelList: modelListString("all")}); err == nil {
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
	for _, value := range []string{"", "ALL", " all", "unknown", "NONE"} {
		if _, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{CompatModelList: modelListString(value)}); err == nil {
			t.Errorf("invalid input %q accepted", value)
		}
	}
	if _, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{}); err == nil {
		t.Error("empty change accepted")
	}
	if _, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{CompatModelList: modelListString("none"), DynamicRoutes: modelListBool(false)}); !errors.Is(err, ErrNothingListed) {
		t.Errorf("none with dynamic routes off accepted: %v", err)
	}
	if calls != 0 {
		t.Fatal("invalid input reached HTTP")
	}
	if _, err = c.GetOrganizationModelList(context.Background()); err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected sanitized GET error: %v", err)
	}
	if _, err = c.SetOrganizationModelList(context.Background(), OrganizationModelListChange{CompatModelList: modelListString("all")}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("unexpected PUT error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("HTTP error triggered retry/fallback: %d calls", calls)
	}
}
