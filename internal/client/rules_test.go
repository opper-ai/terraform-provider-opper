package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const ruleTestID = "11111111-1111-4111-8111-111111111111"

func TestRulesHTTPContract(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer op-mak-test" {
			t.Error("missing bearer credential")
		}
		w.Header().Set("Content-Type", "application/json")
		rule := Rule{ID: ruleTestID, Kind: "comply", Enabled: false, Scope: RuleScope{Type: "org"}, SchemaVersion: 1, Config: json.RawMessage(`{"type":"retention","retention_days":7}`)}
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/management/v1/controls/rules" {
				t.Error(r.URL.Path)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if string(body["idempotency_key"]) != `"terraform:`+ruleTestID+`"` {
				t.Error("create missing stable operation identifier")
			}
			if string(body["id"]) != `"`+ruleTestID+`"` {
				t.Error("create changed rule ID")
			}
			if string(body["enabled"]) != "false" {
				t.Error("false must be explicit")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rule, "meta": map[string]any{}})
		case http.MethodGet:
			if r.URL.Path != "/management/v1/controls/rules" {
				t.Error("rule read must use list")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []Rule{rule}, "meta": map[string]any{"total_count": 1}})
		case http.MethodPatch:
			if r.URL.Path != "/management/v1/controls/rules/"+ruleTestID {
				t.Error(r.URL.Path)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 1 || string(body["enabled"]) != "true" {
				t.Error("PATCH must contain only changed enabled field")
			}
			rule.Enabled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rule})
		case http.MethodDelete:
			if r.URL.Path != "/management/v1/controls/rules/"+ruleTestID {
				t.Error(r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	c, err := New(server.URL, "op-mak-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	input := Rule{ID: ruleTestID, Kind: "comply", Scope: RuleScope{Type: "org"}, SchemaVersion: 1, Config: json.RawMessage(`{"type":"retention","retention_days":7}`)}
	if _, err = c.CreateRule(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetRule(context.Background(), ruleTestID)
	if err != nil || got.ID != ruleTestID {
		t.Fatalf("GetRule: %v %+v", err, got)
	}
	enabled := true
	if _, err = c.UpdateRule(context.Background(), ruleTestID, RulePatch{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteRule(context.Background(), ruleTestID); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("got %d requests", calls)
	}
}

func TestRuleReadMissingVersusFailedList(t *testing.T) {
	for _, code := range []int{200, 403, 404, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"meta":{"total_count":0},"data":[]}`))
			}))
			defer server.Close()
			c, _ := New(server.URL, "test", "test")
			_, err := c.GetRule(context.Background(), ruleTestID)
			if errors.Is(err, ErrRuleNotFound) != (code == 200) {
				t.Fatalf("list status %d was misclassified as missing: %v", code, err)
			}
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRuleResponsesAreValidated(t *testing.T) {
	for name, body := range map[string]string{
		"missing":        `{}`,
		"null":           `{"data":null}`,
		"invalid id":     `{"data":[{"id":"not-a-uuid","kind":"comply","scope":{"type":"org"},"schema_version":1,"config":{}}]}`,
		"invalid scope":  `{"data":[{"id":"` + ruleTestID + `","kind":"comply","scope":{"type":"projects"},"schema_version":1,"config":{}}]}`,
		"empty object":   `{"data":[{}]}`,
		"invalid config": `{"data":[{"id":"` + ruleTestID + `","kind":"comply","scope":{"type":"org"},"schema_version":1,"config":null}]}`,
		"duplicate":      `{"data":[{"id":"` + ruleTestID + `","kind":"comply","scope":{"type":"org"},"schema_version":1,"config":{}},{"id":"` + ruleTestID + `","kind":"comply","scope":{"type":"org"},"schema_version":1,"config":{}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			c, _ := New(server.URL, "test", "test")
			if _, err := c.GetRule(context.Background(), ruleTestID); err == nil || errors.Is(err, ErrRuleNotFound) {
				t.Fatal("accepted invalid rule list or misclassified it as a missing rule")
			}
		})
	}
}
