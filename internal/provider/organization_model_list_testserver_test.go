package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const organizationModelListTestUUID = "11111111-1111-4111-8111-111111111111"
const organizationModelListOtherUUID = "22222222-2222-4222-8222-222222222222"

type organizationModelListTestAPI struct {
	mu                     sync.Mutex
	server                 *httptest.Server
	uuid, value            string
	reads, puts            int
	getFailure, putFailure int
	putResponse            string
	unexpected             []string
}

func newOrganizationModelListTestAPI(t *testing.T) *organizationModelListTestAPI {
	t.Helper()
	api := &organizationModelListTestAPI{uuid: organizationModelListTestUUID, value: "recommended"}
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
func (api *organizationModelListTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fail := func(code int) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "organization preference unavailable"})
	}
	if r.URL.Path != "/management/v1/organization/model-list" || r.URL.RawQuery != "" || (r.Method != http.MethodGet && r.Method != http.MethodPut) {
		api.unexpected = append(api.unexpected, r.Method+" "+r.URL.RequestURI())
		fail(405)
		return
	}
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		fail(401)
		return
	}
	responseUUID := api.uuid
	switch r.Method {
	case http.MethodGet:
		api.reads++
		if api.getFailure != 0 {
			fail(api.getFailure)
			return
		}
	case http.MethodPut:
		api.puts++
		if api.putFailure != 0 {
			fail(api.putFailure)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || (body["compat_model_list"] != "all" && body["compat_model_list"] != "simplified" && body["compat_model_list"] != "recommended") {
			api.unexpected = append(api.unexpected, "invalid PUT body")
			fail(400)
			return
		}
		api.value = body["compat_model_list"]
		switch api.putResponse {
		case "malformed_saved":
			_, _ = w.Write([]byte(`{"meta":{},"data":`))
			return
		case "malformed_other":
			api.value = "recommended"
			_, _ = w.Write([]byte(`{"data":`))
			return
		case "different_value":
			api.value = "recommended"
		case "different_uuid":
			responseUUID = organizationModelListOtherUUID
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": map[string]string{"organization_uuid": responseUUID, "compat_model_list": api.value}})
}
