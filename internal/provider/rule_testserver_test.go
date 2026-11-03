package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

type ruleTestAPI struct {
	mu                                        sync.Mutex
	server                                    *httptest.Server
	rules                                     map[string]client.Rule
	creates, updates, deletes, reads          int
	listFailure, updateFailure, deleteFailure int
	createFailureAfterWrite                   bool
	concurrentConfigOnPatch                   string
	patches                                   []map[string]json.RawMessage
}

func newRuleTestAPI(t *testing.T) *ruleTestAPI {
	t.Helper()
	api := &ruleTestAPI{rules: map[string]client.Rule{}}
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(api.server.Close)
	return api
}
func (api *ruleTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		http.Error(w, "unauthenticated", 401)
		return
	}
	const path = "/management/v1/controls/rules"
	id := strings.TrimPrefix(r.URL.Path, path+"/")
	writeRule := func(code int, rule client.Rule) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rule, "meta": map[string]any{}})
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == path:
		api.reads++
		if api.listFailure != 0 {
			http.Error(w, "unable to list", api.listFailure)
			return
		}
		out := make([]client.Rule, 0, len(api.rules))
		for _, rule := range api.rules {
			out = append(out, rule)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out, "meta": map[string]any{"total_count": len(out)}})
	case r.Method == http.MethodPost && r.URL.Path == path:
		var body struct {
			client.Rule
			IdempotencyKey string `json:"idempotency_key"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !uuidPattern.MatchString(body.ID) || body.IdempotencyKey != "terraform:"+body.ID {
			http.Error(w, "invalid rule", 400)
			return
		}
		if _, ok := api.rules[body.ID]; ok {
			http.Error(w, "duplicate", 409)
			return
		}
		body.CreatedAt = "2026-11-03T10:00:00Z"
		body.UpdatedAt = body.CreatedAt
		body.Config = normalizeTestRuleConfig(body.Rule)
		api.rules[body.ID] = body.Rule
		api.creates++
		if api.createFailureAfterWrite {
			http.Error(w, "create response unavailable", 500)
			return
		}
		writeRule(201, body.Rule)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, path+"/"):
		if api.updateFailure != 0 {
			http.Error(w, "unable to update", api.updateFailure)
			return
		}
		rule, ok := api.rules[id]
		if !ok {
			http.Error(w, "missing", 404)
			return
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		for field, raw := range body {
			switch field {
			case "name":
				if json.Unmarshal(raw, &rule.Name) != nil {
					http.Error(w, "invalid name", 400)
					return
				}
			case "enabled":
				if json.Unmarshal(raw, &rule.Enabled) != nil {
					http.Error(w, "invalid enabled", 400)
					return
				}
			case "schema_version":
				if json.Unmarshal(raw, &rule.SchemaVersion) != nil {
					http.Error(w, "invalid schema", 400)
					return
				}
			case "config":
				rule.Config = append(json.RawMessage(nil), raw...)
			default:
				http.Error(w, "immutable field", 400)
				return
			}
		}
		if api.concurrentConfigOnPatch != "" {
			rule.Config = json.RawMessage(api.concurrentConfigOnPatch)
			api.concurrentConfigOnPatch = ""
		}
		rule.Config = normalizeTestRuleConfig(rule)
		api.updates++
		rule.UpdatedAt = time.Date(2026, 11, 3, 10, 0, api.updates, 0, time.UTC).Format(time.RFC3339)
		api.rules[id] = rule
		api.patches = append(api.patches, body)
		writeRule(200, rule)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, path+"/"):
		if api.deleteFailure != 0 {
			http.Error(w, "unable to delete", api.deleteFailure)
			return
		}
		if _, ok := api.rules[id]; !ok {
			http.Error(w, "missing", 404)
			return
		}
		delete(api.rules, id)
		api.deletes++
		w.WriteHeader(204)
	default:
		http.Error(w, "missing endpoint", 404)
	}
}

// Observe normalization follows controls.go: sampling {} becomes explicit all,
// and inactive rate parameters are pruned. Guard defaults and generated handles
// likewise demonstrate why JSON formatting equality alone cannot track state.
func normalizeTestRuleConfig(rule client.Rule) json.RawMessage {
	var cfg map[string]any
	_ = json.Unmarshal(rule.Config, &cfg)
	if rule.Kind == "observe" {
		if sampling, ok := cfg["sampling"].(map[string]any); ok {
			strategy, _ := sampling["strategy"].(string)
			if strategy == "" || strategy == "all" {
				cfg["sampling"] = map[string]any{"strategy": "all"}
			}
		}
	}
	if rule.Kind == "guard" {
		if cfg["kind"] == nil {
			cfg["kind"] = "guard"
		}
		if cfg["action"] == nil {
			cfg["action"] = "flag"
		}
		if cfg["handle"] == nil {
			cfg["handle"] = "terraform-" + rule.ID[:8]
		}
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return out
}
func (api *ruleTestAPI) drift(id string, enabled bool, config string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	rule := api.rules[id]
	rule.Enabled = enabled
	rule.Config = json.RawMessage(config)
	rule.Config = normalizeTestRuleConfig(rule)
	api.rules[id] = rule
}
func (api *ruleTestAPI) externalDelete(id string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	delete(api.rules, id)
}
