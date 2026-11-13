package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func allowedIPsOpenAPITestSpec() map[string]any {
	array := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 100}
	request := func(ref string) any {
		return map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + ref}}}}
	}
	return map[string]any{"openapi": "3.1.0", "paths": map[string]any{
		"/management/v1/projects/{uuid}/api-keys":      map[string]any{"post": map[string]any{"requestBody": request("Create")}},
		"/management/v1/projects/{uuid}/api-keys/{id}": map[string]any{"patch": map[string]any{"requestBody": request("Update")}},
	}, "components": map[string]any{"schemas": map[string]any{
		"Create": map[string]any{"type": "object", "properties": map[string]any{"allowed_ips": array}},
		"Update": map[string]any{"type": "object", "properties": map[string]any{"allowed_ips": array}, "required": []string{"allowed_ips"}},
	}}}
}

func TestNormalizeAllowedIPsCanonicalizesWithoutChangingInput(t *testing.T) {
	input := []string{" 203.0.113.7 ", "198.51.100.9/24", "2001:0db8::1/128", "2001:db8:abcd:1::/48", "::ffff:192.0.2.4", "203.0.113.7/32", "::ffff:198.51.100.0/120"}
	before := append([]string{}, input...)
	got, err := NormalizeAllowedIPs(input)
	want := []string{"192.0.2.4", "198.51.100.0/24", "2001:db8::1", "2001:db8:abcd::/48", "203.0.113.7"}
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(input, before) {
		t.Fatalf("normalization=%v err=%v; input changed=%t", got, err, !reflect.DeepEqual(input, before))
	}
	for _, empty := range [][]string{nil, {}} {
		got, err := NormalizeAllowedIPs(empty)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty normalization must return explicit []: %v/%v", got, err)
		}
	}
	for _, valid := range []string{"8.0.0.1/8", "100.0.0.0/8", "2001:db8::/16", "::ffff:8.0.0.1/104", "255.255.255.255"} {
		if _, err := NormalizeAllowedIPs([]string{valid}); err != nil {
			t.Errorf("backend-compatible public input %q refused: %v", valid, err)
		}
	}
}

func TestNormalizeAllowedIPsRefusesBackendInvalidEntries(t *testing.T) {
	for _, value := range []string{"", " ", "not-an-ip", "203.0.113.300", "0.0.0.0/0", "::/0", "8.0.0.0/7", "2001::/15", "10.1.2.3", "192.168.0.0/16", "fd00::1", "127.0.0.1", "::1", "169.254.1.1", "fe80::1", "239.1.2.3", "224.0.0.1", "0.0.0.0", "100.64.1.1", "fe80::1%eth0", "::ffff:10.0.0.1", "::ffff:198.51.100.0/95", "198.51.100.7/33"} {
		if _, err := NormalizeAllowedIPs([]string{"203.0.113.7", value}); !IsLocalValidationError(err) {
			t.Errorf("invalid input %q: %v", value, err)
		}
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = "203.0.113.7"
	}
	if _, err := NormalizeAllowedIPs(many); !IsLocalValidationError(err) {
		t.Fatal("entry bound must be checked before deduplication")
	}
}

func TestCreateAPIKeyAllowedIPsPreflightAndCanonicalBody(t *testing.T) {
	var requests []string
	spec := allowedIPsOpenAPITestSpec()
	spec["x-padding"] = strings.Repeat("x", maxResponseBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == "GET" && r.URL.Path == "/v3/openapi.json" {
			_ = json.NewEncoder(w).Encode(spec)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/management/v1/projects/project-id/api-keys" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := map[string]any{"name": "service", "kind": "project", "return_secret": true, "idempotency_key": "stable-command", "allowed_ips": []any{"198.51.100.0/24", "203.0.113.7"}}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("wrong create fields: %v", body)
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"meta":{"idempotency_replayed":false},"data":{"id":42,"name":"service","kind":"project","key":"one-time-secret","allowed_ips":["203.0.113.7","198.51.100.0/24"]}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "test-management-secret", "test")
	key, err := c.CreateAPIKeyWithAllowedIPs(context.Background(), "project-id", "service", "stable-command", []string{"203.0.113.7/32", "198.51.100.9/24", "203.0.113.7"})
	if err != nil || key.ID != 42 || key.Key != "one-time-secret" || !reflect.DeepEqual(key.AllowedIPs, []string{"198.51.100.0/24", "203.0.113.7"}) {
		t.Fatalf("create failed: id=%d err=%v", key.ID, err)
	}
	if !reflect.DeepEqual(requests, []string{"GET /v3/openapi.json", "POST /management/v1/projects/project-id/api-keys"}) {
		t.Fatalf("unsafe request sequence: %v", requests)
	}
}

func TestCreateAPIKeyAllowedIPsFailsClosedBeforePOST(t *testing.T) {
	valid := allowedIPsOpenAPITestSpec()
	oldSpec := allowedIPsOpenAPITestSpec()
	delete(oldSpec["components"].(map[string]any)["schemas"].(map[string]any)["Create"].(map[string]any)["properties"].(map[string]any), "allowed_ips")
	missingPatch := allowedIPsOpenAPITestSpec()
	delete(missingPatch["paths"].(map[string]any), "/management/v1/projects/{uuid}/api-keys/{id}")
	unresolved := allowedIPsOpenAPITestSpec()
	unresolved["components"].(map[string]any)["schemas"].(map[string]any)["Create"] = map[string]any{"$ref": "#/components/schemas/Missing"}
	external := allowedIPsOpenAPITestSpec()
	external["components"].(map[string]any)["schemas"].(map[string]any)["Update"] = map[string]any{"$ref": "https://example.test/schema"}
	wrongType := allowedIPsOpenAPITestSpec()
	wrongType["components"].(map[string]any)["schemas"].(map[string]any)["Create"].(map[string]any)["properties"].(map[string]any)["allowed_ips"] = map[string]any{"type": "string"}
	for name, test := range map[string]struct {
		code int
		spec any
		raw  string
	}{
		"old": {200, oldSpec, ""}, "no patch": {200, missingPatch, ""}, "unresolved": {200, unresolved, ""}, "external": {200, external, ""}, "wrong type": {200, wrongType, ""},
		"missing": {404, valid, ""}, "denied": {403, valid, ""}, "unavailable": {503, valid, ""}, "malformed": {200, nil, "{"}, "empty": {200, nil, "null"}, "oversized": {200, nil, strings.Repeat(" ", 8*1024*1024+1)},
	} {
		t.Run(name, func(t *testing.T) {
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				w.WriteHeader(test.code)
				if test.raw != "" {
					_, _ = io.WriteString(w, test.raw)
				} else {
					_ = json.NewEncoder(w).Encode(test.spec)
				}
			}))
			defer server.Close()
			c, _ := New(server.URL, "secret", "test")
			key, err := c.CreateAPIKeyWithAllowedIPs(context.Background(), "project-id", "service", "stable-command", []string{"203.0.113.7"})
			if err == nil || key.ID != 0 || !reflect.DeepEqual(methods, []string{"GET"}) {
				t.Fatalf("unsupported spec allowed mutation: id=%d err=%v methods=%v", key.ID, err, methods)
			}
		})
	}
}

func TestCreateAPIKeyAllowedIPsUnconfirmedPolicyRetainsMintedIdentityAndSecret(t *testing.T) {
	for name, fields := range map[string]string{
		"missing": "", "null": `,"allowed_ips":null`, "empty": `,"allowed_ips":[]`, "different": `,"allowed_ips":["198.51.100.7"]`, "invalid": `,"allowed_ips":["127.0.0.1"]`, "wrong type": `,"allowed_ips":"203.0.113.7"`, "wrong name": `,"allowed_ips":["203.0.113.7"]`, "wrong kind": `,"allowed_ips":["203.0.113.7"]`,
	} {
		t.Run(name, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method)
				if r.Method == "GET" {
					_ = json.NewEncoder(w).Encode(allowedIPsOpenAPITestSpec())
					return
				}
				responseName, kind := "service", "project"
				if name == "wrong name" {
					responseName = "other"
				}
				if name == "wrong kind" {
					kind = "personal"
				}
				_, _ = fmt.Fprintf(w, `{"data":{"id":42,"name":%q,"kind":%q,"key":"one-time-secret"%s}}`, responseName, kind, fields)
			}))
			defer server.Close()
			c, _ := New(server.URL, "secret", "test")
			key, err := c.CreateAPIKeyWithAllowedIPs(context.Background(), "project-id", "service", "", []string{"203.0.113.7"})
			if err == nil || key.ID != 42 || key.Key != "one-time-secret" || strings.Contains(err.Error(), "secret") || !reflect.DeepEqual(requests, []string{"GET", "POST"}) {
				t.Fatalf("unconfirmed creation lost recovery or retried: id=%d err=%v requests=%v", key.ID, err, requests)
			}
		})
	}
}

func TestAPIKeyAllowedIPsListPresenceAndUnexpectedSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":1,"kind":"project","key":"discard"},{"id":2,"kind":"project","allowed_ips":[],"key":"discard"},{"id":3,"kind":"personal","allowed_ips":["203.0.113.7/32","198.51.100.2/24"]}]}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	keys, err := c.ListAPIKeys(context.Background(), "project-id")
	if err != nil || len(keys) != 3 || keys[0].AllowedIPs != nil || keys[1].AllowedIPs == nil || len(keys[1].AllowedIPs) != 0 || !reflect.DeepEqual(keys[2].AllowedIPs, []string{"198.51.100.0/24", "203.0.113.7"}) || keys[0].Key != "" || keys[1].Key != "" {
		t.Fatal("list lost missing/empty distinction, normalization, or secret stripping")
	}
}

func TestLegacyCreateAPIKeyMalformedPresentPolicyRetainsMintedKey(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/management/v1/projects/project-id/api-keys" {
			t.Errorf("legacy creation made an unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":{"id":42,"name":"service","kind":"project","key":"recover-this-secret","allowed_ips":{"private-secret-canary":true}}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	key, err := c.CreateAPIKey(context.Background(), "project-id", "service", "stable-command")
	if err == nil || key.ID != 42 || key.Key != "recover-this-secret" || requests != 1 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("post-mint malformed metadata lost recovery: id=%d err=%v requests=%d", key.ID, err, requests)
	}
}

func TestCreateAPIKeyExplicitEmptyAllowedIPsAndReplay(t *testing.T) {
	for _, replay := range []bool{false, true} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.Method != "POST" {
				t.Error("explicit unrestricted creation need not perform discovery")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			ips, ok := body["allowed_ips"].([]any)
			if !ok || len(ips) != 0 || len(body) != 4 {
				t.Error("empty policy was not explicit or empty idempotency key was sent")
			}
			secret := ""
			if !replay {
				secret = "one-time-secret"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"idempotency_replayed": replay}, "data": map[string]any{"id": 42, "name": "service", "kind": "project", "key": secret, "allowed_ips": []string{}}})
		}))
		c, _ := New(server.URL, "management-secret", "test")
		key, err := c.CreateAPIKeyWithAllowedIPs(context.Background(), "project-id", "service", "", nil)
		server.Close()
		if err != nil || key.ID != 42 || key.AllowedIPs == nil || len(key.AllowedIPs) != 0 || key.IdempotencyReplayed != replay || requests != 1 || (replay && key.Key != "") {
			t.Fatalf("explicit empty/replay handling: id=%d err=%v requests=%d", key.ID, err, requests)
		}
	}
}

func TestAPIKeyAllowedIPsLocalFailuresNeverRequestOrEchoInput(t *testing.T) {
	calls := 0
	c, _ := New("https://api.opper.ai", "management-secret", "test")
	c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})
	secret := "sensitive-input-canary"
	for _, id := range []string{"", "..", "a/b", "a?b"} {
		if _, err := c.CreateAPIKeyWithAllowedIPs(context.Background(), id, "service", "", []string{"203.0.113.7"}); err == nil {
			t.Error("invalid project path accepted")
		}
		if _, err := c.UpdateAPIKeyAllowedIPs(context.Background(), id, 42, []string{}); err == nil {
			t.Error("invalid PATCH project path accepted")
		}
	}
	for _, id := range []int64{0, -1, 2147483648} {
		if _, err := c.UpdateAPIKeyAllowedIPs(context.Background(), "project-id", id, []string{}); !IsLocalValidationError(err) {
			t.Error("invalid runtime key ID accepted")
		}
	}
	_, createErr := c.CreateAPIKeyWithAllowedIPs(context.Background(), "project-id", "service", "", []string{secret})
	_, updateErr := c.UpdateAPIKeyAllowedIPs(context.Background(), "project-id", 42, []string{secret})
	for _, err := range []error{createErr, updateErr} {
		if !IsLocalValidationError(err) || strings.Contains(err.Error(), secret) {
			t.Fatal("invalid input was not safely rejected before a request")
		}
	}
	if calls != 0 {
		t.Fatalf("invalid arguments made %d requests", calls)
	}
}

func TestCreateAPIKeyAllowedIPsPOSTFailureNeverRetriesOrRemediates(t *testing.T) {
	for _, code := range []int{403, 409, 429, 500, 503} {
		var methods []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			methods = append(methods, r.Method)
			if r.Method == "GET" {
				_ = json.NewEncoder(w).Encode(allowedIPsOpenAPITestSpec())
				return
			}
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"error":"private-response-canary"}`)
		}))
		c, _ := New(server.URL, "management-secret", "test")
		key, err := c.CreateAPIKeyWithAllowedIPs(context.Background(), "project-id", "service", "", []string{"203.0.113.7"})
		server.Close()
		var apiError *APIError
		if !errors.As(err, &apiError) || apiError.StatusCode != code || key.ID != 0 || strings.Contains(err.Error(), "canary") || !reflect.DeepEqual(methods, []string{"GET", "POST"}) {
			t.Fatalf("POST failure was retried/remediated or exposed body: err=%v requests=%v", err, methods)
		}
	}
}

func TestCreateAPIKeyAllowedIPsPreflightTransportFailurePreservesCause(t *testing.T) {
	calls := 0
	cause := errors.New("https://user:password@example.test/private-transport-canary")
	c, _ := New("https://api.opper.ai", "management-secret", "test")
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v3/openapi.json" {
			t.Error("failed discovery attempted mutation")
		}
		return nil, cause
	})
	key, err := c.CreateAPIKeyWithAllowedIPs(context.Background(), "project-id", "service", "", []string{"203.0.113.7"})
	if !errors.Is(err, cause) || key.ID != 0 || calls != 1 || strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "password") {
		t.Fatalf("preflight transport recovery/secret handling: id=%d err=%v calls=%d", key.ID, err, calls)
	}
}

func TestUpdateAPIKeyAllowedIPsCanonicalClearAndConfirmation(t *testing.T) {
	for _, ips := range [][]string{nil, {}, {"203.0.113.7/32", "198.51.100.1/24"}} {
		var calls int
		want, _ := NormalizeAllowedIPs(ips)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != "PATCH" || r.URL.Path != "/management/v1/projects/project-id/api-keys/42" {
				t.Error("wrong PATCH path")
			}
			var body map[string][]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["allowed_ips"] == nil || !reflect.DeepEqual(body["allowed_ips"], want) {
				t.Error("PATCH did not send canonical explicit replacement")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 42, "name": "service", "kind": "project", "allowed_ips": want, "key": "discard"}})
		}))
		c, _ := New(server.URL, "secret", "test")
		key, err := c.UpdateAPIKeyAllowedIPs(context.Background(), "project-id", 42, ips)
		server.Close()
		if err != nil || key.ID != 42 || !reflect.DeepEqual(key.AllowedIPs, want) || key.Key != "" || calls != 1 {
			t.Fatalf("PATCH confirmation failed: id=%d err=%v calls=%d", key.ID, err, calls)
		}
	}
}

func TestUpdateAPIKeyAllowedIPsDoesNotRetryOrTrustUnconfirmedResponses(t *testing.T) {
	for name, response := range map[string]string{"missing": `{"id":42,"kind":"project"}`, "null": `{"id":42,"kind":"project","allowed_ips":null}`, "different": `{"id":42,"kind":"project","allowed_ips":[]}`, "identity": `{"id":43,"kind":"project","allowed_ips":["203.0.113.7"]}`, "kind": `{"id":42,"kind":"personal","allowed_ips":["203.0.113.7"]}`, "bad list": `{"id":42,"kind":"project","allowed_ips":true}`} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = fmt.Fprintf(w, `{"data":%s}`, response) }))
			defer server.Close()
			c, _ := New(server.URL, "secret", "test")
			_, err := c.UpdateAPIKeyAllowedIPs(context.Background(), "project-id", 42, []string{"203.0.113.7"})
			if err == nil || calls != 1 {
				t.Fatalf("unconfirmed PATCH accepted or retried: %v/%d", err, calls)
			}
		})
	}
	for _, code := range []int{401, 403, 404, 409, 429, 500, 503} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"error":"private-secret-canary"}`)
		}))
		c, _ := New(server.URL, "secret", "test")
		_, err := c.UpdateAPIKeyAllowedIPs(context.Background(), "project-id", 42, []string{})
		server.Close()
		var apiError *APIError
		if !errors.As(err, &apiError) || apiError.StatusCode != code || calls != 1 || strings.Contains(err.Error(), "canary") {
			t.Fatalf("PATCH status/secret/retry handling: %v/%d", err, calls)
		}
	}
}
