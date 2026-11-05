package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const customTestID = "11111111-1111-4111-8111-111111111111"
const customTestOtherID = "22222222-2222-4222-8222-222222222222"
const customTestToken = "cmv1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func customTestModel(id string) map[string]any {
	return map[string]any{"id": id, "name": "model", "identifier": "openai/gpt-test", "provider": "openai", "type": "llm", "credential_present": true, "created_at": "2026-11-05T00:00:00Z", "updated_at": "2026-11-05T00:00:00Z"}
}
func customTestCreation(key, id string, exists bool) map[string]any {
	return map[string]any{"idempotency_key": key, "custom_model_uuid": id, "resource_exists": exists}
}
func customTestReceipt(key, id string, exists bool, matches any) map[string]any {
	r := customTestCreation(key, id, exists)
	r["result_matches_current_state"] = matches
	return r
}

func TestCustomModelCRUDAndRecovery(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer management-secret" {
			t.Error("missing auth")
		}
		var body map[string]any
		if r.Method == "POST" || r.Method == "PATCH" || r.Method == "DELETE" {
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("missing JSON")
			}
		}
		var result any
		switch {
		case r.Method == "POST":
			if len(body) != 4 || body["credential"] != "private-credential" || body["idempotency_key"] != "create-1" {
				t.Error("incorrect creation intent")
			}
			w.WriteHeader(201)
			result = map[string]any{"meta": map[string]any{"idempotency_replayed": false, "operation": customTestCreation("create-1", customTestID, true)}, "data": customTestModel(customTestID)}
		case r.Method == "PATCH":
			if len(body) != 3 || body["name"] != "renamed" || body["expected_change_token"] != customTestToken || body["idempotency_key"] != "update-1" {
				t.Error("PATCH broadened")
			}
			result = map[string]any{"meta": map[string]any{"idempotency_replayed": false}, "data": customTestReceipt("update-1", customTestID, true, true)}
		case r.Method == "DELETE":
			if len(body) != 1 || body["expected_change_token"] != customTestToken {
				t.Error("DELETE lost precondition")
			}
			w.WriteHeader(204)
			return
		case strings.Contains(r.URL.Path, "/update-operations/"):
			result = map[string]any{"meta": map[string]any{}, "data": customTestReceipt("update-1", customTestID, true, nil)}
		case strings.Contains(r.URL.Path, "/operations/"):
			result = map[string]any{"meta": map[string]any{}, "data": customTestCreation("create-1", customTestID, false)}
		case strings.HasSuffix(r.URL.Path, "/state"):
			result = map[string]any{"meta": map[string]any{"change_token": customTestToken}, "data": customTestModel(customTestID)}
		default:
			result = map[string]any{"meta": map[string]any{}, "data": customTestModel(customTestID)}
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	ctx := context.Background()
	created, err := c.CreateCustomModel(ctx, "model", "openai/gpt-test", "private-credential", "create-1")
	if err != nil || created.Model.ID != customTestID || created.Replayed {
		t.Fatalf("create: %v", err)
	}
	state, err := c.GetCustomModelState(ctx, customTestID)
	if err != nil || state.ChangeToken != customTestToken {
		t.Fatalf("state: %v", err)
	}
	if _, err := c.GetCustomModel(ctx, customTestID); err != nil {
		t.Fatal(err)
	}
	u, err := c.UpdateCustomModel(ctx, customTestID, CustomModelUpdateInput{ExpectedChangeToken: state.ChangeToken, IdempotencyKey: "update-1", Name: stringPtr("renamed")})
	if err != nil || u.Operation.CustomModelUUID != customTestID || u.Replayed {
		t.Fatalf("update: %v", err)
	}
	r, err := c.GetCustomModelUpdateOperation(ctx, "update-1")
	if err != nil || r.ResultMatchesCurrentState != nil {
		t.Fatalf("nullable recovery: %v", err)
	}
	rc, err := c.GetCustomModelCreation(ctx, "create-1")
	if err != nil || rc.ResourceExists {
		t.Fatalf("deleted creation recovery: %v", err)
	}
	if err := c.DeleteCustomModel(ctx, customTestID, state.ChangeToken); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 7 {
		t.Fatalf("unexpected retries: %v", methods)
	}
}

func TestCustomModelListPaginationAndStableOrder(t *testing.T) {
	var pages int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/management/v1/custom-models" || r.URL.Query().Get("limit") != "100" {
			t.Error("unexpected list request")
		}
		offset := 0
		if pages == 1 {
			offset = 100
		}
		if r.URL.Query().Get("offset") != fmt.Sprint(offset) {
			t.Error("incorrect offset")
		}
		items := []any{}
		for i := offset; i < 101 && i < offset+100; i++ {
			items = append(items, customTestModel(fmt.Sprintf("%08x-1111-4111-8111-111111111111", 101-i)))
		}
		pages++
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"offset": offset, "limit": 100, "total_count": 101}, "data": items})
	}))
	defer server.Close()
	c, _ := New(server.URL, "key", "test")
	models, err := c.ListCustomModels(context.Background())
	if err != nil || len(models) != 101 || pages != 2 || models[0].ID != "00000001-1111-4111-8111-111111111111" {
		t.Fatalf("incomplete or unstable list: %v", err)
	}
}

func TestCustomModelStrictResponsesAndNoCredentialEcho(t *testing.T) {
	model := customTestModel(customTestID)
	valid, _ := json.Marshal(map[string]any{"meta": map[string]any{}, "data": model})
	for label, body := range map[string]string{
		"missing boolean": strings.Replace(string(valid), `"credential_present":true,`, "", 1),
		"null boolean":    strings.Replace(string(valid), `"credential_present":true`, `"credential_present":null`, 1),
		"wrong UUID":      strings.Replace(string(valid), customTestID, customTestOtherID, 1),
		"unknown secret":  strings.Replace(string(valid), `"name":"model"`, `"name":"model","credential":"secret-canary"`, 1),
		"duplicate":       strings.Replace(string(valid), `"name":"model"`, `"name":"model","name":"duplicate"`, 1),
		"trailing":        string(valid) + ` {}`,
	} {
		t.Run(label, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New(server.URL, "key", "test")
			_, err := c.GetCustomModel(context.Background(), customTestID)
			if err == nil || strings.Contains(err.Error(), "secret-canary") {
				t.Fatal("invalid response accepted or leaked")
			}
		})
	}
}

func TestCustomModelDeleteRequiresExact204(t *testing.T) {
	for _, code := range []int{200, 201, 202, 400, 403, 409, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(code)
				fmt.Fprint(w, `{"credential":"secret-canary"}`)
			}))
			defer server.Close()
			c, _ := New(server.URL, "key", "test")
			err := c.DeleteCustomModel(context.Background(), customTestID, customTestToken)
			if err == nil || strings.Contains(err.Error(), "secret-canary") || calls != 1 {
				t.Fatal("unsafe deletion acknowledgement or retry")
			}
		})
	}
}

func TestCustomModelInputValidationBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	c, _ := New(server.URL, "key", "test")
	ctx := context.Background()
	for _, id := range []string{"", "not-a-uuid", strings.ToUpper(customTestOtherID[:8]) + "-AAAA-4222-8222-222222222222", "00000000-0000-0000-0000-000000000000", "../projects", customTestID + "?scope=all"} {
		if _, err := c.GetCustomModel(ctx, id); err == nil {
			t.Error("invalid GET identity accepted")
		}
		if _, err := c.GetCustomModelState(ctx, id); err == nil {
			t.Error("invalid state identity accepted")
		}
		if err := c.DeleteCustomModel(ctx, id, customTestToken); err == nil {
			t.Error("invalid DELETE identity accepted")
		}
	}
	for _, key := range []string{"", "not/key", strings.Repeat("k", 129), "k?x=1"} {
		if _, err := c.GetCustomModelCreation(ctx, key); err == nil {
			t.Error("invalid creation recovery key accepted")
		}
		if _, err := c.GetCustomModelUpdateOperation(ctx, key); err == nil {
			t.Error("invalid update recovery key accepted")
		}
		if _, err := c.CreateCustomModel(ctx, "name", "openai/model", "credential", key); err == nil {
			t.Error("invalid creation operation accepted")
		}
	}
	for _, in := range []CustomModelUpdateInput{
		{ExpectedChangeToken: customTestToken, IdempotencyKey: "update"},
		{ExpectedChangeToken: "wrong", IdempotencyKey: "update", Name: stringPtr("name")},
		{ExpectedChangeToken: customTestToken, IdempotencyKey: "", Name: stringPtr("name")},
		{ExpectedChangeToken: customTestToken, IdempotencyKey: "update", Name: stringPtr(" ")},
		{ExpectedChangeToken: customTestToken, IdempotencyKey: "update", Identifier: stringPtr("bedrock/model")},
		{ExpectedChangeToken: customTestToken, IdempotencyKey: "update", Credential: stringPtr("")},
		{ExpectedChangeToken: customTestToken, IdempotencyKey: "update", Credential: stringPtr(strings.Repeat("x", 8193))},
	} {
		if _, err := c.UpdateCustomModel(ctx, customTestID, in); err == nil {
			t.Error("invalid patch accepted")
		}
	}
	for _, args := range [][3]string{{" ", "openai/model", "credential"}, {strings.Repeat("x", 256), "openai/model", "credential"}, {"name", "openai/../x", "credential"}, {"name", "openai/model", " "}, {"name", "openai/model", string([]byte{0xff})}} {
		if _, err := c.CreateCustomModel(ctx, args[0], args[1], args[2], "create"); err == nil {
			t.Error("invalid creation accepted")
		}
	}
	if err := c.DeleteCustomModel(ctx, customTestID, "bad"); err == nil {
		t.Error("invalid delete precondition accepted")
	}
	if calls != 0 {
		t.Fatalf("invalid inputs reached network %d times", calls)
	}
}

func TestCustomModelIncompleteListRefused(t *testing.T) {
	for _, which := range []string{"missing total", "null rows", "empty partial", "duplicate", "offset", "limit", "overrun", "bound", "negative", "total changed", "cross-page duplicate"} {
		t.Run(which, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				meta := map[string]any{"offset": 0, "limit": 100, "total_count": 1}
				var rows any = []any{customTestModel(customTestID)}
				switch which {
				case "missing total":
					delete(meta, "total_count")
				case "null rows":
					rows = nil
				case "empty partial":
					rows = []any{}
				case "duplicate":
					meta["total_count"] = 2
					rows = []any{customTestModel(customTestID), customTestModel(customTestID)}
				case "offset":
					meta["offset"] = 1
				case "limit":
					meta["limit"] = 99
				case "overrun":
					meta["total_count"] = 0
				case "bound":
					meta["total_count"] = 10001
				case "negative":
					meta["total_count"] = -1
				case "total changed":
					meta["total_count"] = 2
					if calls > 0 {
						meta["offset"] = 1
						meta["total_count"] = 3
						rows = []any{customTestModel(customTestOtherID)}
					}
				case "cross-page duplicate":
					meta["total_count"] = 2
					if calls > 0 {
						meta["offset"] = 1
					}
				}
				calls++
				_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta, "data": rows})
			}))
			defer server.Close()
			c, _ := New(server.URL, "key", "test")
			if _, err := c.ListCustomModels(context.Background()); err == nil {
				t.Fatal("accepted incomplete inventory")
			}
			if calls > 2 {
				t.Fatal("unbounded pagination")
			}
		})
	}
}

func TestCustomModelStateStrictnessAndNullableMetadata(t *testing.T) {
	for _, which := range []string{"valid legacy nulls", "missing token", "invalid token", "null token", "unknown meta", "wrong identity", "missing provider", "null timestamp"} {
		t.Run(which, func(t *testing.T) {
			model := customTestModel(customTestID)
			model["provider"] = nil
			model["type"] = nil
			meta := map[string]any{"change_token": customTestToken}
			switch which {
			case "missing token":
				delete(meta, "change_token")
			case "invalid token":
				meta["change_token"] = "bad"
			case "null token":
				meta["change_token"] = nil
			case "unknown meta":
				meta["credential"] = "private-canary"
			case "wrong identity":
				model["id"] = customTestOtherID
			case "missing provider":
				delete(model, "provider")
			case "null timestamp":
				model["updated_at"] = nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta, "data": model})
			}))
			defer server.Close()
			c, _ := New(server.URL, "key", "test")
			s, err := c.GetCustomModelState(context.Background(), customTestID)
			if which == "valid legacy nulls" {
				if err != nil || s.Model.Provider != nil || s.Model.Type != nil {
					t.Fatalf("legacy metadata refused: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid state accepted")
			}
		})
	}
}

func TestCustomModelHistoricalReplayAndReceiptValidation(t *testing.T) {
	for _, which := range []string{"historical exists", "historical deleted", "unknown signing key", "new false match", "missing replay flag", "null exists", "missing nullable match", "wrong key", "wrong identity", "secret extra"} {
		t.Run(which, func(t *testing.T) {
			meta := map[string]any{"idempotency_replayed": true}
			row := customTestReceipt("update", customTestID, true, false)
			switch which {
			case "historical deleted":
				row["resource_exists"] = false
			case "unknown signing key":
				row["result_matches_current_state"] = nil
			case "new false match":
				meta["idempotency_replayed"] = false
			case "missing replay flag":
				delete(meta, "idempotency_replayed")
			case "null exists":
				row["resource_exists"] = nil
			case "missing nullable match":
				delete(row, "result_matches_current_state")
			case "wrong key":
				row["idempotency_key"] = "other"
			case "wrong identity":
				row["custom_model_uuid"] = customTestOtherID
			case "secret extra":
				row["credential"] = "private-canary"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta, "data": row})
			}))
			defer server.Close()
			c, _ := New(server.URL, "key", "test")
			result, err := c.UpdateCustomModel(context.Background(), customTestID, CustomModelUpdateInput{ExpectedChangeToken: customTestToken, IdempotencyKey: "update", Credential: stringPtr("private-new-credential")})
			valid := which == "historical exists" || which == "historical deleted" || which == "unknown signing key"
			if valid {
				if err != nil || !result.Replayed {
					t.Fatalf("historical receipt refused: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid update receipt accepted")
			}
		})
	}
}

func TestCustomModelCreationStatusAndIdentityValidation(t *testing.T) {
	for _, which := range []string{"new", "replay later metadata", "status mismatch", "wrong receipt UUID", "wrong key", "missing exists", "missing replay flag", "wrong new name"} {
		t.Run(which, func(t *testing.T) {
			status := 201
			model := customTestModel(customTestID)
			op := customTestCreation("create", customTestID, true)
			meta := map[string]any{"operation": op, "idempotency_replayed": false}
			switch which {
			case "replay later metadata":
				status = 200
				meta["idempotency_replayed"] = true
				model["name"] = "later-name"
				model["provider"] = nil
			case "status mismatch":
				status = 200
			case "wrong receipt UUID":
				op["custom_model_uuid"] = customTestOtherID
			case "wrong key":
				op["idempotency_key"] = "wrong"
			case "missing exists":
				delete(op, "resource_exists")
			case "missing replay flag":
				delete(meta, "idempotency_replayed")
			case "wrong new name":
				model["name"] = "wrong"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta, "data": model})
			}))
			defer server.Close()
			c, _ := New(server.URL, "key", "test")
			result, err := c.CreateCustomModel(context.Background(), "model", "openai/gpt-test", "private-credential", "create")
			if which == "new" || which == "replay later metadata" {
				if err != nil {
					t.Fatal(err)
				}
				if result.Replayed != (status == 200) {
					t.Fatal("replay mismatch")
				}
			} else if err == nil {
				t.Fatal("invalid creation receipt accepted")
			}
		})
	}
}

func TestCustomModelPrivateInputRedacted(t *testing.T) {
	in := CustomModelUpdateInput{ExpectedChangeToken: customTestToken, IdempotencyKey: "operation", Credential: stringPtr("private-credential")}
	for _, out := range []string{fmt.Sprint(in), fmt.Sprintf("%+v", in), fmt.Sprintf("%#v", in), fmt.Sprint(&in)} {
		if strings.Contains(out, "private-credential") || strings.Contains(out, customTestToken) {
			t.Fatal("formatter exposed private input")
		}
	}
	raw, err := json.Marshal(in)
	if err != nil || strings.Contains(string(raw), "private-credential") || strings.Contains(string(raw), customTestToken) {
		t.Fatal("JSON exposed private input")
	}
}

type customModelTransport func(*http.Request) (*http.Response, error)

func (f customModelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type customModelFailReader struct{}

func (customModelFailReader) Read([]byte) (int, error) {
	return 0, errors.New("private-read-error-canary")
}
func (customModelFailReader) Close() error { return nil }

func TestCustomModelTransportAndBoundsFailSafely(t *testing.T) {
	for _, which := range []string{"transport", "canceled", "read", "oversize"} {
		t.Run(which, func(t *testing.T) {
			c, _ := New("https://api.opper.ai", "private-management-key", "test")
			calls := 0
			c.httpClient.Transport = customModelTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if which == "transport" {
					return nil, errors.New("private-transport-error-canary")
				}
				if which == "canceled" {
					return nil, context.Canceled
				}
				var body io.ReadCloser = customModelFailReader{}
				if which == "oversize" {
					body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1)))
				}
				return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
			})
			_, err := c.GetCustomModel(context.Background(), customTestID)
			if err == nil || IsLocalValidationError(err) || strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "private-management-key") || calls != 1 {
				t.Fatal("transport error leaked, retried or accepted")
			}
			if which == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost")
			}
		})
	}
}

func TestCustomModelLocalValidationClassification(t *testing.T) {
	ctx := context.Background()
	c, _ := New("https://api.opper.ai", "private-management-key", "test")
	calls := 0
	c.httpClient.Transport = customModelTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected transport call")
	})
	check := func(t *testing.T, err error, message string) {
		t.Helper()
		if err == nil || !IsLocalValidationError(err) || err.Error() != message || calls != 0 {
			t.Fatalf("expected classified no-request failure %q, got %v (requests %d)", message, err, calls)
		}
		if !IsLocalValidationError(fmt.Errorf("wrapped: %w", err)) {
			t.Fatal("local classification lost through wrapping")
		}
	}
	for _, id := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-000000000000", customTestID + "?private=canary"} {
		t.Run("identity/"+id, func(t *testing.T) {
			_, err := c.GetCustomModel(ctx, id)
			check(t, err, "invalid custom model UUID")
			_, err = c.GetCustomModelState(ctx, id)
			check(t, err, "invalid custom model UUID")
			_, err = c.UpdateCustomModel(ctx, id, CustomModelUpdateInput{ExpectedChangeToken: customTestToken, IdempotencyKey: "update", Name: stringPtr("valid")})
			check(t, err, "invalid custom model UUID")
			check(t, c.DeleteCustomModel(ctx, id, customTestToken), "invalid custom model UUID")
		})
	}
	for _, key := range []string{"", "private/key", strings.Repeat("k", 129)} {
		t.Run("key/"+key, func(t *testing.T) {
			_, err := c.GetCustomModelCreation(ctx, key)
			check(t, err, "invalid custom model creation operation key")
			_, err = c.GetCustomModelCreationState(ctx, key)
			check(t, err, "invalid custom model creation operation key")
			_, err = c.GetCustomModelUpdateOperation(ctx, key)
			check(t, err, "invalid custom model update operation key")
			_, err = c.CreateCustomModel(ctx, "valid", "openai/model", "private-credential", key)
			check(t, err, "invalid custom model creation intent")
			_, err = c.CreateCustomModelWithState(ctx, "valid", "openai/model", "private-credential", key)
			check(t, err, "invalid custom model creation intent")
			_, err = c.UpdateCustomModel(ctx, customTestID, CustomModelUpdateInput{ExpectedChangeToken: customTestToken, IdempotencyKey: key, Name: stringPtr("valid")})
			check(t, err, "invalid custom model update intent")
		})
	}
	for _, in := range []struct{ label, name, identifier, credential string }{
		{"Unicode blank name", "\u00a0\u2003", "openai/model", "private-credential"},
		{"invalid UTF-8 name", string([]byte{0xff}), "openai/model", "private-credential"},
		{"long name", strings.Repeat("界", 256), "openai/model", "private-credential"},
		{"invalid identifier", "valid", "private/identifier", "private-credential"},
		{"long identifier", "valid", "openai/" + strings.Repeat("m", 250), "private-credential"},
		{"oversized ASCII credential", "valid", "openai/model", strings.Repeat("x", 8193)},
		{"oversized Unicode credential", "valid", "openai/model", strings.Repeat("é", 4097)},
		{"blank credential", "valid", "openai/model", "\u00a0\u2003"},
		{"invalid UTF-8 credential", "valid", "openai/model", string([]byte{0xff})},
	} {
		t.Run(in.label, func(t *testing.T) {
			_, err := c.CreateCustomModel(ctx, in.name, in.identifier, in.credential, "create")
			check(t, err, "invalid custom model creation intent")
			_, err = c.CreateCustomModelWithState(ctx, in.name, in.identifier, in.credential, "create")
			check(t, err, "invalid custom model creation intent")
			_, err = c.UpdateCustomModel(ctx, customTestID, CustomModelUpdateInput{ExpectedChangeToken: customTestToken, IdempotencyKey: "update", Name: &in.name, Identifier: &in.identifier, Credential: &in.credential})
			check(t, err, "invalid custom model update intent")
		})
	}
	for _, token := range []string{"", "private-token-canary", "cmv1_" + strings.Repeat("A", 44)} {
		_, err := c.UpdateCustomModel(ctx, customTestID, CustomModelUpdateInput{ExpectedChangeToken: token, IdempotencyKey: "update", Name: stringPtr("valid")})
		check(t, err, "invalid custom model update intent")
		check(t, c.DeleteCustomModel(ctx, customTestID, token), "invalid custom model deletion precondition")
	}
	_, err := c.UpdateCustomModel(ctx, customTestID, CustomModelUpdateInput{ExpectedChangeToken: customTestToken, IdempotencyKey: "update"})
	check(t, err, "invalid custom model update intent")
	_, _, err = c.customModelRequest(ctx, http.MethodPost, customModelsPath, make(chan int), 201)
	check(t, err, "could not encode custom model request")
	c.baseURL = ":invalid-private-origin"
	_, err = c.CreateCustomModelWithState(ctx, "valid", "openai/model", "private-credential", "create")
	check(t, err, "could not construct custom model request")
	if IsLocalValidationError(nil) || IsLocalValidationError(errors.New("invalid custom model creation intent")) {
		t.Fatal("untyped or nil error classified as local validation")
	}
}

func TestCustomModelResponseErrorsAreNotLocalValidation(t *testing.T) {
	for _, code := range []int{201, 403} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			c, _ := New("https://api.opper.ai", "private-management-key", "test")
			calls := 0
			c.httpClient.Transport = customModelTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(`{"private-response":"canary"}`)), Header: make(http.Header)}, nil
			})
			_, err := c.CreateCustomModelWithState(context.Background(), "valid", "openai/model", "private-credential", "create")
			if err == nil || IsLocalValidationError(err) || strings.Contains(err.Error(), "canary") || calls != 1 {
				t.Fatalf("post-request failure incorrectly classified or leaked: %v", err)
			}
		})
	}
}
