package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCreateAPIKey(t *testing.T) {
	for _, replayed := range []bool{false, true} {
		t.Run(fmt.Sprint(replayed), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/management/v1/projects/project-id/api-keys" {
					t.Errorf("request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer management-secret" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing headers")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["name"] != "service-key" || body["kind"] != "project" || body["return_secret"] != true || body["idempotency_key"] != "operation-1" || len(body) != 4 {
					t.Errorf("unexpected request fields")
				}
				if replayed {
					fmt.Fprint(w, `{"meta":{"idempotency_replayed":true,"key_retrievable":false},"data":{"id":42,"name":"service-key","prefix":"op-key","kind":"project","created_at":"2026-11-03T00:00:00Z"}}`)
				} else {
					w.WriteHeader(http.StatusCreated)
					fmt.Fprint(w, `{"meta":{},"data":{"id":42,"name":"service-key","prefix":"op-key","kind":"project","created_at":"2026-11-03T00:00:00Z","key":"runtime-secret"}}`)
				}
			}))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			key, err := c.CreateAPIKey(context.Background(), "project-id", "service-key", "operation-1")
			if err != nil {
				t.Fatal(err)
			}
			if key.ID != 42 || key.Name != "service-key" || key.Kind != "project" || key.CreatedAt == "" || key.IdempotencyReplayed != replayed {
				t.Fatal("wrong metadata")
			}
			if replayed && key.Key != "" {
				t.Fatal("replay included secret")
			}
			if !replayed && key.Key != "runtime-secret" {
				t.Fatal("initial secret missing")
			}
		})
	}
}

func TestCreateAPIKeyOptionalIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["idempotency_key"]; ok {
			t.Error("empty idempotency key sent")
		}
		fmt.Fprint(w, `{"data":{"id":1,"name":"key","kind":"project","key":"secret"}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	if _, err := c.CreateAPIKey(context.Background(), "project-id", "key", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAPIKeyInvalidEnvelope(t *testing.T) {
	for _, body := range []string{``, `{`, `null`, `{}`, `{"data":null}`, `{"data":{}}`, `{"data":{"id":0,"kind":"project","key":"runtime-secret"}}`, `{"data":{"id":1,"kind":"personal","key":"runtime-secret"}}`, `{"data":{"id":1,"kind":"project","key":99}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			if _, err := c.CreateAPIKey(context.Background(), "project-id", "key", ""); err == nil || strings.Contains(err.Error(), "runtime-secret") {
				t.Fatal("invalid envelope accepted or secret leaked")
			}
		})
	}
}

func TestListAPIKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/management/v1/projects/project-id/api-keys" {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"meta":{"total_count":2},"data":[{"id":1,"name":"one","prefix":"op-one","kind":"personal"},{"id":2,"name":"two","prefix":"op-two","kind":"project"}]}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	keys, err := c.ListAPIKeys(context.Background(), "project-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ID != 1 || keys[0].Kind != "personal" || keys[1].ID != 2 || keys[1].Kind != "project" {
		t.Fatal("wrong metadata list")
	}
}

func TestListAPIKeysEmptyAndInvalidEnvelope(t *testing.T) {
	for _, test := range []struct {
		body  string
		valid bool
	}{
		{`{"data":[]}`, true},
		{`{}`, false}, {`null`, false}, {`{"data":null}`, false}, {`{"data":{}}`, false}, {`{"data":[{"id":0}]}`, false}, {`{"data":[{"id":1}]} trailing`, false},
	} {
		t.Run(test.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, test.body) }))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			keys, err := c.ListAPIKeys(context.Background(), "project-id")
			if test.valid {
				if err != nil || len(keys) != 0 {
					t.Fatal("empty list rejected")
				}
			} else if err == nil {
				t.Fatal("invalid list accepted")
			}
		})
	}
}

func TestDeleteAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/management/v1/projects/project-id/api-keys/42" {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	if err := c.DeleteAPIKey(context.Background(), "project-id", 42); err != nil {
		t.Fatal(err)
	}
}

func TestAPIKeyErrorsAndNoRetries(t *testing.T) {
	for _, code := range []int{401, 403, 404, 409, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(code)
				fmt.Fprint(w, `{"error":"management-secret runtime-secret"}`)
			}))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			_, createErr := c.CreateAPIKey(context.Background(), "project-id", "key", "operation-1")
			_, listErr := c.ListAPIKeys(context.Background(), "project-id")
			deleteErr := c.DeleteAPIKey(context.Background(), "project-id", 42)
			for _, err := range []error{createErr, listErr, deleteErr} {
				if err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) || strings.Contains(err.Error(), "secret") || IsNotFound(err) != (code == 404) {
					t.Fatal("wrong error or secret leaked")
				}
			}
			if requests.Load() != 3 {
				t.Fatal("requests retried")
			}
		})
	}
}

func TestAPIKeyInvalidPaths(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	for _, id := range []string{"", "..", "a/b", "a?b"} {
		if _, err := c.CreateAPIKey(context.Background(), id, "key", ""); err == nil {
			t.Error("accepted invalid project")
		}
		if _, err := c.ListAPIKeys(context.Background(), id); err == nil {
			t.Error("accepted invalid project")
		}
		if err := c.DeleteAPIKey(context.Background(), id, 42); err == nil {
			t.Error("accepted invalid project")
		}
	}
	for _, id := range []int64{0, -1} {
		if err := c.DeleteAPIKey(context.Background(), "project-id", id); err == nil {
			t.Error("accepted invalid key ID")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid paths reached API")
	}
}

func TestCreateAPIKeyMissingSecretPreservesMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"id":42,"name":"key","prefix":"op-key","kind":"project"}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	key, err := c.CreateAPIKey(context.Background(), "project-id", "key", "operation-1")
	if err != nil || key.ID != 42 || key.Key != "" {
		t.Fatal("missing secret prevented retaining resource metadata")
	}
}

func TestListAPIKeysDiscardsUnexpectedSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":42,"name":"key","kind":"project","key":"unexpected-secret"}]}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	keys, err := c.ListAPIKeys(context.Background(), "project-id")
	if err != nil || len(keys) != 1 || keys[0].Key != "" {
		t.Fatal("list retained unexpected secret")
	}
}
