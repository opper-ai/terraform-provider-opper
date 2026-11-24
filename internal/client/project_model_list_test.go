package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const projectModelListTestUUID = "33333333-3333-4333-8333-333333333333"

func TestProjectModelListClient(t *testing.T) {
	var requests []string
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing credential")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /management/v1/organization/model-list/projects":
			// The second item predates compat_model_list_enforced.
			_, _ = w.Write([]byte(`{"meta":{"total_count":2},"data":[
{"project_uuid":"33333333-3333-4333-8333-333333333333","project_name":"Support bot","compat_model_list":"recommended","compat_model_list_dynamic_routes":false,"compat_model_list_enforced":true},
{"project_uuid":"44444444-4444-4444-8444-444444444444","project_name":"","compat_model_list":"none","compat_model_list_dynamic_routes":true}]}`))
		case "PUT /management/v1/projects/" + projectModelListTestUUID + "/model-list":
			var body map[string]any
			data, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(data, &body); err != nil {
				t.Error(err)
			}
			bodies = append(bodies, body)
			enforced, _ := body["compat_model_list_enforced"].(bool)
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": map[string]any{"project_uuid": projectModelListTestUUID, "project_name": "Support bot", "compat_model_list": body["compat_model_list"], "compat_model_list_dynamic_routes": body["compat_model_list_dynamic_routes"], "compat_model_list_enforced": enforced}})
		case "DELETE /management/v1/projects/" + projectModelListTestUUID + "/model-list":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	all, err := c.ListProjectModelLists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ProjectModelList{
		{ProjectUUID: projectModelListTestUUID, ProjectName: "Support bot", CompatModelList: "recommended", DynamicRoutes: false, Enforced: true},
		{ProjectUUID: "44444444-4444-4444-8444-444444444444", ProjectName: "", CompatModelList: "none", DynamicRoutes: true, Enforced: false},
	}
	if len(all) != 2 || all[0] != want[0] || all[1] != want[1] {
		t.Fatalf("unexpected lists: %+v", all)
	}
	got, found, err := c.GetProjectModelList(ctx, projectModelListTestUUID)
	if err != nil || !found || got != want[0] {
		t.Fatalf("unexpected project list: %+v %v %v", got, found, err)
	}
	if _, found, err = c.GetProjectModelList(ctx, "55555555-5555-4555-8555-555555555555"); err != nil || found {
		t.Fatalf("a project without its own list was found: %v %v", found, err)
	}
	got, err = c.SetProjectModelList(ctx, projectModelListTestUUID, ProjectModelListChange{CompatModelList: "simplified", DynamicRoutes: true})
	if err != nil || got.CompatModelList != "simplified" || !got.DynamicRoutes || got.Enforced {
		t.Fatalf("unexpected PUT result: %+v %v", got, err)
	}
	enforced := true
	if _, err = c.SetProjectModelList(ctx, projectModelListTestUUID, ProjectModelListChange{CompatModelList: "all", DynamicRoutes: false, Enforced: &enforced}); err != nil {
		t.Fatal(err)
	}
	if err = c.ClearProjectModelList(ctx, projectModelListTestUUID); err != nil {
		t.Fatal(err)
	}
	// List and routes are always sent; enforced only when set.
	if len(bodies) != 2 || len(bodies[0]) != 2 || len(bodies[1]) != 3 || bodies[1]["compat_model_list_enforced"] != true {
		t.Fatalf("unexpected PUT bodies: %v", bodies)
	}
	if len(requests) != 6 {
		t.Fatalf("unexpected requests: %v", requests)
	}
}

func TestProjectModelListClientInvalidInput(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	c, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{"", "not-a-uuid", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", projectModelListTestUUID + "/../x"} {
		if _, err = c.SetProjectModelList(ctx, id, ProjectModelListChange{CompatModelList: "all", DynamicRoutes: true}); err == nil {
			t.Errorf("invalid project UUID %q accepted", id)
		}
		if err = c.ClearProjectModelList(ctx, id); err == nil {
			t.Errorf("invalid project UUID %q accepted for DELETE", id)
		}
		if _, _, err = c.GetProjectModelList(ctx, id); err == nil {
			t.Errorf("invalid project UUID %q accepted for GET", id)
		}
	}
	for _, value := range []string{"", "ALL", "unknown"} {
		if _, err = c.SetProjectModelList(ctx, projectModelListTestUUID, ProjectModelListChange{CompatModelList: value, DynamicRoutes: true}); err == nil {
			t.Errorf("invalid list %q accepted", value)
		}
	}
	if _, err = c.SetProjectModelList(ctx, projectModelListTestUUID, ProjectModelListChange{CompatModelList: "none"}); !errors.Is(err, ErrNothingListed) {
		t.Errorf("none with routes off accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid input reached HTTP: %d calls", calls)
	}
}

func TestProjectModelListClientRejectsInvalidResponse(t *testing.T) {
	const item = `{"project_uuid":"33333333-3333-4333-8333-333333333333","project_name":"P","compat_model_list":"all","compat_model_list_dynamic_routes":true}`
	for _, body := range []string{
		`{}`, `{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":`,
		`{"data":[` + item + `,` + item + `]}`,
		`{"data":[{"project_uuid":"not-a-uuid","project_name":"P","compat_model_list":"all","compat_model_list_dynamic_routes":true}]}`,
		`{"data":[{"project_uuid":"33333333-3333-4333-8333-333333333333","compat_model_list":"all","compat_model_list_dynamic_routes":true}]}`,
		`{"data":[{"project_uuid":"33333333-3333-4333-8333-333333333333","project_name":"P","compat_model_list":"unknown","compat_model_list_dynamic_routes":true}]}`,
		`{"data":[{"project_uuid":"33333333-3333-4333-8333-333333333333","project_name":"P","compat_model_list":"all"}]}`,
		`{"data":[{"project_uuid":"33333333-3333-4333-8333-333333333333","project_name":"P","compat_model_list":"all","compat_model_list_dynamic_routes":true,"compat_model_list_enforced":"true"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			c, err := New(server.URL, "test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.ListProjectModelLists(context.Background()); err == nil {
				t.Fatal("invalid list response accepted")
			}
		})
	}
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":[` + item + `]}`, `{"data":{"project_uuid":"44444444-4444-4444-8444-444444444444","project_name":"P","compat_model_list":"all","compat_model_list_dynamic_routes":true}}`} {
		t.Run("PUT "+body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			c, err := New(server.URL, "test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.SetProjectModelList(context.Background(), projectModelListTestUUID, ProjectModelListChange{CompatModelList: "all", DynamicRoutes: true}); err == nil {
				t.Fatal("invalid PUT response accepted")
			}
		})
	}
}

func TestProjectModelListClientHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"private details must not be exposed"}`))
	}))
	defer server.Close()
	c, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SetProjectModelList(context.Background(), projectModelListTestUUID, ProjectModelListChange{CompatModelList: "all", DynamicRoutes: true})
	if !IsNotFound(err) || strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected PUT error: %v", err)
	}
	if err = c.ClearProjectModelList(context.Background(), projectModelListTestUUID); !IsNotFound(err) {
		t.Fatalf("unexpected DELETE error: %v", err)
	}
}
