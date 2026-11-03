package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

const keyTestProjectID = "11111111-1111-4111-8111-111111111111"

type apiKeyTestAPI struct {
	mu                      sync.Mutex
	server                  *httptest.Server
	keys                    map[int64]client.APIKey
	idempotency             map[string]int64
	nextID                  int64
	creates, reads, deletes int
	events                  []string
	failures                map[string]int
	projectExists           bool
	omitNextSecret          bool
}

func newAPIKeyTestAPI(t *testing.T) *apiKeyTestAPI {
	t.Helper()
	a := &apiKeyTestAPI{keys: make(map[int64]client.APIKey), idempotency: make(map[string]int64), nextID: 41, failures: make(map[string]int), projectExists: true}
	a.server = httptest.NewServer(http.HandlerFunc(a.serveHTTP))
	t.Cleanup(a.server.Close)
	return a
}

func (a *apiKeyTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
		return
	}
	if code := a.failures[r.Method]; code != 0 {
		w.WriteHeader(code)
		fmt.Fprint(w, `{"error":"management-token-must-not-leak"}`)
		return
	}
	if !a.projectExists {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"project not found"}`)
		return
	}
	base := "/management/v1/projects/" + keyTestProjectID + "/api-keys"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			a.reads++
			keys := make([]client.APIKey, 0, len(a.keys))
			for _, key := range a.keys {
				key.Key = ""
				keys = append(keys, key)
			}
			json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"total_count": len(keys)}, "data": keys})
		case http.MethodPost:
			var body struct {
				Name           string `json:"name"`
				Kind           string `json:"kind"`
				ReturnSecret   bool   `json:"return_secret"`
				IdempotencyKey string `json:"idempotency_key"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Kind != "project" || !body.ReturnSecret {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"expected named project-owned key and explicit secret request"}`)
				return
			}
			if body.IdempotencyKey != "" {
				if id, ok := a.idempotency[body.IdempotencyKey]; ok {
					existing, exists := a.keys[id]
					if !exists || existing.Name != body.Name {
						w.WriteHeader(409)
						fmt.Fprint(w, `{"error":"idempotency conflict"}`)
						return
					}
					existing.Key = ""
					json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"idempotency_replayed": true, "key_retrievable": false}, "data": existing})
					return
				}
			}
			a.nextID++
			key := client.APIKey{ID: a.nextID, Name: body.Name, Kind: "project", Prefix: fmt.Sprintf("op-test-%d", a.nextID), Key: fmt.Sprintf("op-test-secret-%d", a.nextID), CreatedAt: "2026-11-03T09:00:00Z"}
			a.keys[key.ID] = key
			if body.IdempotencyKey != "" {
				a.idempotency[body.IdempotencyKey] = key.ID
			}
			a.creates++
			a.events = append(a.events, fmt.Sprintf("create:%d", key.ID))
			if a.omitNextSecret {
				key.Key = ""
				a.omitNextSecret = false
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": key})
		default:
			w.WriteHeader(405)
		}
		return
	}
	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, base+"/") {
		id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, base+"/"), 10, 64)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		if _, ok := a.keys[id]; !ok {
			w.WriteHeader(404)
			return
		}
		delete(a.keys, id)
		a.deletes++
		a.events = append(a.events, fmt.Sprintf("delete:%d", id))
		w.WriteHeader(204)
		return
	}
	w.WriteHeader(404)
}

func (a *apiKeyTestAPI) externalDelete(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	numericID, _ := strconv.ParseInt(id, 10, 64)
	delete(a.keys, numericID)
}
func (a *apiKeyTestAPI) setFailure(method string, code int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[method] = code
}
func (a *apiKeyTestAPI) seedPersonal() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	a.keys[a.nextID] = client.APIKey{ID: a.nextID, Name: "personal", Prefix: "op-personal", Kind: "personal", CreatedAt: "2026-11-03T09:00:00Z"}
	return a.nextID
}
