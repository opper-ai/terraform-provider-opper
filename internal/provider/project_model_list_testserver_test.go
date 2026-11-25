package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

const projectModelListTestUUID = "33333333-3333-4333-8333-333333333333"
const projectModelListOtherUUID = "44444444-4444-4444-8444-444444444444"
const projectModelListMissingUUID = "55555555-5555-4555-8555-555555555555"

type projectModelListEntry struct {
	list            string
	routes, enforce bool
}

// projectModelListTestAPI mirrors the project model-list routes of the
// Management API: the list of projects with their own list, PUT and DELETE.
type projectModelListTestAPI struct {
	mu       sync.Mutex
	server   *httptest.Server
	projects map[string]string // live project UUID -> name
	lists    map[string]*projectModelListEntry
	// The organization's list, consulted only while it is enforced.
	orgList                               string
	orgRoutes, orgEnforced                bool
	putBodies                             []map[string]any
	reads, puts, deletes                  int
	getFailure, putFailure, deleteFailure int
	putResponse                           string
	unexpected                            []string
}

func newProjectModelListTestAPI(t *testing.T) *projectModelListTestAPI {
	t.Helper()
	api := &projectModelListTestAPI{
		projects: map[string]string{projectModelListTestUUID: "Support bot", projectModelListOtherUUID: "Batch jobs"},
		lists:    map[string]*projectModelListEntry{},
		orgList:  "all", orgRoutes: true,
	}
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(api.server.Close)
	t.Cleanup(func() {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.unexpected) > 0 {
			t.Errorf("unexpected API operations: %v", api.unexpected)
		}
	})
	return api
}

func projectModelListRank(view string) int {
	return map[string]int{"none": 0, "recommended": 1, "simplified": 2, "all": 3}[view]
}

func (api *projectModelListTestAPI) entryJSON(uuid string) map[string]any {
	e := api.lists[uuid]
	return map[string]any{"project_uuid": uuid, "project_name": api.projects[uuid], "compat_model_list": e.list, "compat_model_list_dynamic_routes": e.routes, "compat_model_list_enforced": e.enforce}
}

func (api *projectModelListTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fail := func(code int) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "project model list unavailable"})
	}
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		fail(401)
		return
	}
	if r.URL.RawQuery == "" && r.Method == http.MethodGet && r.URL.Path == "/management/v1/organization/model-list/projects" {
		api.reads++
		if api.getFailure != 0 {
			fail(api.getFailure)
			return
		}
		uuids := make([]string, 0, len(api.lists))
		for uuid := range api.lists {
			uuids = append(uuids, uuid)
		}
		sort.Strings(uuids)
		data := make([]map[string]any, 0, len(uuids))
		for _, uuid := range uuids {
			data = append(data, api.entryJSON(uuid))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"total_count": len(data)}, "data": data})
		return
	}
	uuid, ok := strings.CutPrefix(r.URL.Path, "/management/v1/projects/")
	uuid, ok2 := strings.CutSuffix(uuid, "/model-list")
	if !ok || !ok2 || r.URL.RawQuery != "" || (r.Method != http.MethodPut && r.Method != http.MethodDelete) {
		api.unexpected = append(api.unexpected, r.Method+" "+r.URL.RequestURI())
		fail(405)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		api.deletes++
		if api.deleteFailure != 0 {
			fail(api.deleteFailure)
			return
		}
		if _, live := api.projects[uuid]; !live {
			fail(404)
			return
		}
		delete(api.lists, uuid)
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPut:
		api.puts++
		if api.putFailure != 0 {
			fail(api.putFailure)
			return
		}
		// Mirror the API: the list and the routes are required, enforced is
		// optional, nothing else, never null, and never a result that lists
		// nothing.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.unexpected = append(api.unexpected, "invalid PUT body")
			fail(400)
			return
		}
		api.putBodies = append(api.putBodies, body)
		next := projectModelListEntry{}
		if previous := api.lists[uuid]; previous != nil {
			next.enforce = previous.enforce
		}
		list, listOK := body["compat_model_list"].(string)
		routes, routesOK := body["compat_model_list_dynamic_routes"].(bool)
		if !listOK || !routesOK || projectModelListRank(list) == 0 && list != "none" {
			api.unexpected = append(api.unexpected, "PUT without a valid list and routes")
			fail(400)
			return
		}
		next.list, next.routes = list, routes
		for key, raw := range body {
			switch key {
			case "compat_model_list", "compat_model_list_dynamic_routes":
			case "compat_model_list_enforced":
				v, ok := raw.(bool)
				if !ok {
					api.unexpected = append(api.unexpected, "invalid compat_model_list_enforced")
					fail(400)
					return
				}
				next.enforce = v
			default:
				api.unexpected = append(api.unexpected, "unknown PUT field "+key)
				fail(400)
				return
			}
		}
		if _, live := api.projects[uuid]; !live {
			fail(404)
			return
		}
		if next.list == "none" && !next.routes {
			api.unexpected = append(api.unexpected, "PUT would list nothing")
			fail(400)
			return
		}
		// While the organization enforces, a project list wider than its own
		// is refused. This is an expected refusal, not a provider bug.
		if api.orgEnforced && (projectModelListRank(next.list) > projectModelListRank(api.orgList) || next.routes && !api.orgRoutes) {
			fail(400)
			return
		}
		api.lists[uuid] = &next
		switch api.putResponse {
		case "malformed_saved":
			_, _ = w.Write([]byte(`{"meta":{},"data":`))
			return
		case "different_value":
			api.lists[uuid].list = "recommended"
		case "different_uuid":
			response := api.entryJSON(uuid)
			response["project_uuid"] = projectModelListOtherUUID
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": response})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": api.entryJSON(uuid)})
	}
}
