package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func customStateCreationReceipt(key, id string, exists bool, token string) map[string]any {
	r := customTestCreation(key, id, exists)
	r["initial_change_token"] = token
	return r
}

func TestCustomModelCreationStateRetainsInitialProof(t *testing.T) {
	var calls []string
	newer := "cmv1_" + strings.Repeat("B", 43)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/management/v1/custom-models/creations":
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 4 || body["credential"] != "private-creation-credential" || body["idempotency_key"] != "create-state" {
				t.Error("creation intent changed")
			}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": customTestModel(customTestID), "meta": map[string]any{"operation": customStateCreationReceipt("create-state", customTestID, true, customTestToken), "idempotency_replayed": false}})
		case "/management/v1/custom-models/" + customTestID + "/state":
			// A hidden concurrent edit leaves metadata unchanged but changes the
			// complete-state token. The client must retain the original proof.
			_ = json.NewEncoder(w).Encode(map[string]any{"data": customTestModel(customTestID), "meta": map[string]any{"change_token": newer}})
		case "/management/v1/custom-models/creations/create-state":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": customStateCreationReceipt("create-state", customTestID, false, customTestToken), "meta": map[string]any{}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, _ := New(server.URL, "key", "test")
	ctx := context.Background()
	created, err := c.CreateCustomModelWithState(ctx, "model", "openai/gpt-test", "private-creation-credential", "create-state")
	if err != nil || created.Operation.InitialChangeToken != customTestToken || created.Model.ID != customTestID || created.Replayed {
		t.Fatalf("creation proof lost: %v", err)
	}
	current, err := c.GetCustomModelState(ctx, customTestID)
	if err != nil || created.Operation.InitialChangeToken == current.ChangeToken || !reflect.DeepEqual(current.Model, created.Model) {
		t.Fatalf("hidden edit proof missing: %v", err)
	}
	recovered, err := c.GetCustomModelCreationState(ctx, "create-state")
	if err != nil || recovered.ResourceExists || recovered.InitialChangeToken != customTestToken {
		t.Fatalf("deleted proof changed: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("unexpected requests: %v", calls)
	}
}

func TestCustomModelCreationStateOldBackendFailsClosed(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/management/v1/custom-models" {
			t.Error("unguarded legacy creation fallback")
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"private-response-canary"}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "key", "test")
	ctx := context.Background()
	_, err := c.CreateCustomModelWithState(ctx, "model", "openai/gpt-test", "private-credential", "creation")
	if !IsNotFound(err) || strings.Contains(err.Error(), "canary") {
		t.Fatal("old backend was accepted or error leaked")
	}
	_, err = c.GetCustomModelCreationState(ctx, "creation")
	if !IsNotFound(err) {
		t.Fatal("missing new recovery endpoint accepted")
	}
	if !reflect.DeepEqual(calls, []string{"POST /management/v1/custom-models/creations", "GET /management/v1/custom-models/creations/creation"}) {
		t.Fatalf("unexpected fallback/retry: %v", calls)
	}
}

func TestCustomModelCreationStateStrictProofAndReplay(t *testing.T) {
	for _, which := range []string{"new", "historical replay", "missing token", "null token", "bad token", "extra field", "missing exists", "wrong UUID", "wrong key", "status mismatch"} {
		t.Run(which, func(t *testing.T) {
			status := 201
			model := customTestModel(customTestID)
			proof := customStateCreationReceipt("create", customTestID, true, customTestToken)
			meta := map[string]any{"operation": proof, "idempotency_replayed": false}
			switch which {
			case "historical replay":
				status = 200
				meta["idempotency_replayed"] = true
				model["name"] = "later-name"
			case "missing token":
				delete(proof, "initial_change_token")
			case "null token":
				proof["initial_change_token"] = nil
			case "bad token":
				proof["initial_change_token"] = "not-an-original-token"
			case "extra field":
				proof["credential"] = "private-canary"
			case "missing exists":
				delete(proof, "resource_exists")
			case "wrong UUID":
				proof["custom_model_uuid"] = customTestOtherID
			case "wrong key":
				proof["idempotency_key"] = "other-key"
			case "status mismatch":
				status = 200
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": model, "meta": meta})
			}))
			defer server.Close()
			c, _ := New(server.URL, "key", "test")
			result, err := c.CreateCustomModelWithState(context.Background(), "model", "openai/gpt-test", "private-credential", "create")
			valid := which == "new" || which == "historical replay"
			if valid {
				if err != nil || result.Operation.InitialChangeToken != customTestToken {
					t.Fatalf("valid proof refused: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}
