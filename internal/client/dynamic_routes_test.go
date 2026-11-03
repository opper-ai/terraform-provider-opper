package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestDynamicRouteDraftCRUD(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, hasLayout := body["layout"]; hasLayout {
				t.Error("provider overwrote unmanaged editor layout")
			}
			switch r.Method {
			case http.MethodPost:
				if body["deploy"] != false || body["name"] != "app-route" {
					t.Errorf("create must explicitly be draft-only: %#v", body)
				}
				if _, ok := body["graph"].(map[string]any); !ok {
					t.Errorf("graph not encoded as JSON: %#v", body)
				}
				w.WriteHeader(http.StatusCreated)
			case http.MethodPatch:
				if len(body) != 1 || body["display_name"] != "New label" {
					t.Errorf("metadata fields: %#v", body)
				}
			case http.MethodPut:
				if len(body) != 1 || body["description"] != "" {
					t.Errorf("partial draft fields: %#v", body)
				}
			}
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Fprint(w, `{"data":{"route":{"uuid":"uuid","name":"app-route","display_name":"New label","invocation_model":"dynamic/app-route"},"draft":{"graph":{"elements":[]},"revision":1}}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	ctx := context.Background()
	if _, err := c.CreateDynamicRoute(ctx, "app-route", "label", "description", json.RawMessage(`{"elements":[]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetDynamicRoute(ctx, "app-route"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateDynamicRouteDisplayName(ctx, "app-route", "New label"); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if _, err := c.UpdateDynamicRouteDraft(ctx, "app-route", &empty, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDynamicRoute(ctx, "app-route"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /management/v1/dynamic-routes", "GET /management/v1/dynamic-routes/app-route", "PATCH /management/v1/dynamic-routes/app-route", "PUT /management/v1/dynamic-routes/app-route/draft", "DELETE /management/v1/dynamic-routes/app-route"}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestDynamicRouteInvalidResponses(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":{"route":{"uuid":"uuid","name":"other"},"draft":{"graph":{},"revision":1}}}`, `{"data":{"route":{"uuid":"uuid","name":"app"},"draft":{"graph":null,"revision":1}}}`, `{"data":{"route":{"uuid":"uuid","name":"app"},"draft":{"graph":{},"revision":0}}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New(server.URL, "secret", "test")
			if _, err := c.GetDynamicRoute(context.Background(), "app"); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}

func TestDynamicRouteInvalidRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	for _, name := range []string{"", "../x", "UPPER", "dynamic/app", "foo?x=y"} {
		if _, err := c.GetDynamicRoute(context.Background(), name); err == nil {
			t.Errorf("accepted route name %q", name)
		}
	}
	if _, err := c.UpdateDynamicRouteDraft(context.Background(), "app", nil, nil); err == nil {
		t.Error("accepted empty draft update")
	}
	if requests != 0 {
		t.Fatal("invalid requests reached API")
	}
}
