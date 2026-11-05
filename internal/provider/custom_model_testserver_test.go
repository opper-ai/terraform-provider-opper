package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

type customModelFixture struct {
	Model      client.CustomModel
	Credential string
	Revision   int
}

func (m *customModelFixture) token() string {
	value := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s:%s:%s", m.Model.ID, m.Revision, m.Model.Name, m.Model.Identifier, m.Credential)))
	return "cmv1_" + base64.RawURLEncoding.EncodeToString(value[:])
}

type customModelFixtureReceipt struct {
	UUID, InputHash, Token string
	Stateful               bool
}

// The fixture implements the public metadata/state/receipt contracts, including
// historical receipt replay, rather than returning canned provider successes.
type customModelTestAPI struct {
	mu                  sync.Mutex
	server              *httptest.Server
	models              map[string]*customModelFixture
	creationReceipts    map[string]customModelFixtureReceipt
	updateReceipts      map[string]customModelFixtureReceipt
	sequence            int
	creates, updates    int
	rotations, deletes  int
	postAttempts        int
	patchAttempts       int
	deleteAttempts      int
	stateReads          int
	creationReceiptGets int
	updateReceiptGets   int
	getFailure          int
	updateFailure       int
	deleteFailure       int
	creationReceiptFail int
	updateReceiptFail   int
	dropCreate          bool
	hiddenAfterCreate   bool
	stateCreateMissing  bool
	dropUpdate          bool
	blockAfterDrop      bool
	externalBeforePatch bool
	changeAfterUpdate   bool
	unknownMatch        bool
	lastPatchFields     map[string]bool
	requestLog          []string
	unexpected          []string
}

func newCustomModelTestAPI(t *testing.T) *customModelTestAPI {
	t.Helper()
	a := &customModelTestAPI{models: map[string]*customModelFixture{}, creationReceipts: map[string]customModelFixtureReceipt{}, updateReceipts: map[string]customModelFixtureReceipt{}}
	a.server = httptest.NewServer(http.HandlerFunc(a.serveHTTP))
	t.Cleanup(a.server.Close)
	t.Cleanup(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if len(a.unexpected) != 0 {
			t.Errorf("unexpected custom model requests: %v", a.unexpected)
		}
	})
	return a
}

func fixtureInputHash(v map[string]string) string {
	raw, _ := json.Marshal(v)
	hash := sha256.Sum256(raw)
	return fmt.Sprintf("%x", hash)
}

func (a *customModelTestAPI) seedLocked(name, credential string) *customModelFixture {
	a.sequence++
	provider, kind := "openai", "llm"
	m := &customModelFixture{Model: client.CustomModel{ID: fmt.Sprintf("11111111-1111-4111-8111-%012d", a.sequence), Name: name, Identifier: "openai/gpt-test", Provider: &provider, Type: &kind, CredentialPresent: true, CreatedAt: "2026-11-05T12:00:00Z", UpdatedAt: "2026-11-05T12:00:00Z"}, Credential: credential, Revision: 1}
	a.models[m.Model.ID] = m
	return m
}

func (a *customModelTestAPI) seed(name, credential string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.seedLocked(name, credential).Model.ID
}

func (a *customModelTestAPI) externalEdit(id, name, credential string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m := a.models[id]; m != nil {
		if name != "" {
			m.Model.Name = name
		}
		if credential != "" {
			m.Credential = credential
		}
		a.bumpLocked(m)
	}
}

func (a *customModelTestAPI) bumpLocked(m *customModelFixture) {
	m.Revision++
	m.Model.UpdatedAt = fmt.Sprintf("2026-11-05T12:00:%02dZ", m.Revision)
}

func (a *customModelTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	a.requestLog = append(a.requestLog, r.Method+" "+r.URL.RequestURI())
	fail := func(code int) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "custom model request refused; private response canary"})
	}
	respond := func(code int, data any, meta map[string]any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": meta})
	}
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		fail(401)
		return
	}
	const base = "/management/v1/custom-models"
	if !strings.HasPrefix(r.URL.Path, base) {
		a.unexpected = append(a.unexpected, r.Method+" "+r.URL.RequestURI())
		fail(404)
		return
	}
	part := strings.TrimPrefix(r.URL.Path, base)
	if (strings.HasPrefix(part, "/operations/") || strings.HasPrefix(part, "/creations/")) && r.Method == http.MethodGet {
		a.creationReceiptGets++
		stateful := strings.HasPrefix(part, "/creations/")
		if stateful && a.stateCreateMissing {
			fail(404)
			return
		}
		if a.creationReceiptFail != 0 {
			fail(a.creationReceiptFail)
			return
		}
		key := strings.TrimPrefix(strings.TrimPrefix(part, "/operations/"), "/creations/")
		prior, ok := a.creationReceipts[key]
		if !ok || prior.Stateful != stateful {
			fail(404)
			return
		}
		respond(200, a.creationReceiptLocked(key, prior), map[string]any{})
		return
	}
	if strings.HasPrefix(part, "/update-operations/") && r.Method == http.MethodGet {
		a.updateReceiptGets++
		if a.updateReceiptFail != 0 {
			fail(a.updateReceiptFail)
			return
		}
		key := strings.TrimPrefix(part, "/update-operations/")
		prior, ok := a.updateReceipts[key]
		if !ok {
			fail(404)
			return
		}
		respond(200, a.updateReceiptLocked(key, prior), map[string]any{})
		return
	}
	if part == "" && r.Method == http.MethodGet {
		if a.getFailure != 0 {
			fail(a.getFailure)
			return
		}
		ids := make([]string, 0, len(a.models))
		for id := range a.models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		items := []client.CustomModel{}
		for i := offset; i < len(ids) && i < offset+limit; i++ {
			items = append(items, a.models[ids[i]].Model)
		}
		respond(200, items, map[string]any{"offset": offset, "limit": limit, "total_count": len(ids)})
		return
	}
	if r.URL.RawQuery != "" {
		a.unexpected = append(a.unexpected, "query on custom model mutation or state")
		fail(400)
		return
	}
	if (part == "" || part == "/creations") && r.Method == http.MethodPost {
		a.postAttempts++
		stateful := part == "/creations"
		if stateful && a.stateCreateMissing {
			fail(404)
			return
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 4 || body["name"] == "" || body["identifier"] == "" || body["credential"] == "" || body["idempotency_key"] == "" {
			fail(400)
			return
		}
		key, hash := body["idempotency_key"], fixtureInputHash(body)
		if prior, ok := a.creationReceipts[key]; ok {
			m := a.models[prior.UUID]
			if prior.Stateful != stateful || prior.InputHash != hash || m == nil {
				fail(409)
				return
			}
			respond(200, m.Model, map[string]any{"operation": a.creationReceiptLocked(key, prior), "idempotency_replayed": true})
			return
		}
		for _, m := range a.models {
			if m.Model.Name == body["name"] {
				fail(409)
				return
			}
		}
		m := a.seedLocked(body["name"], body["credential"])
		m.Model.Identifier = body["identifier"]
		prior := customModelFixtureReceipt{UUID: m.Model.ID, InputHash: hash, Token: m.token(), Stateful: stateful}
		a.creationReceipts[key] = prior
		a.creates++
		if a.hiddenAfterCreate {
			a.hiddenAfterCreate = false
			// Simulate a legacy credential edit which leaves every public
			// metadata field, including timestamps, byte-for-byte unchanged.
			m.Credential = "hidden-create-credential-fixture"
		}
		if a.dropCreate {
			a.dropCreate = false
			if a.blockAfterDrop {
				a.creationReceiptFail = 503
			}
			fail(502)
			return
		}
		respond(201, m.Model, map[string]any{"operation": a.creationReceiptLocked(key, prior), "idempotency_replayed": false})
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(part, "/"), "/state")
	m := a.models[id]
	if r.Method == http.MethodGet && part != "" {
		a.stateReads++
		if a.getFailure != 0 {
			fail(a.getFailure)
			return
		}
		if m == nil {
			fail(404)
			return
		}
		meta := map[string]any{}
		if strings.HasSuffix(part, "/state") {
			meta["change_token"] = m.token()
		}
		respond(200, m.Model, meta)
		return
	}
	if r.Method == http.MethodDelete {
		a.deleteAttempts++
		if a.deleteFailure != 0 {
			fail(a.deleteFailure)
			return
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["expected_change_token"] == "" {
			fail(400)
			return
		}
		if m != nil && body["expected_change_token"] != m.token() {
			fail(409)
			return
		}
		delete(a.models, id)
		a.deletes++
		w.WriteHeader(204)
		return
	}
	if r.Method == http.MethodPatch {
		a.patchAttempts++
		if a.updateFailure != 0 {
			fail(a.updateFailure)
			return
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["expected_change_token"] == "" || body["idempotency_key"] == "" || len(body) < 3 {
			fail(400)
			return
		}
		key, hash := body["idempotency_key"], fixtureInputHash(body)
		if prior, ok := a.updateReceipts[key]; ok {
			if prior.UUID != id || prior.InputHash != hash {
				fail(409)
				return
			}
			respond(200, a.updateReceiptLocked(key, prior), map[string]any{"idempotency_replayed": true})
			return
		}
		if m == nil {
			fail(404)
			return
		}
		if a.externalBeforePatch {
			a.externalBeforePatch = false
			m.Credential = "external-credential-fixture"
			a.bumpLocked(m)
		}
		if body["expected_change_token"] != m.token() {
			fail(409)
			return
		}
		changed := false
		a.lastPatchFields = map[string]bool{}
		for name := range body {
			a.lastPatchFields[name] = true
			switch name {
			case "expected_change_token", "idempotency_key":
			case "name":
				changed = changed || m.Model.Name != body[name]
				m.Model.Name = body[name]
			case "identifier":
				changed = changed || m.Model.Identifier != body[name]
				m.Model.Identifier = body[name]
			case "credential":
				changed = true
				m.Credential = body[name]
				a.rotations++
			default:
				a.unexpected = append(a.unexpected, "unknown PATCH field "+name)
				fail(400)
				return
			}
		}
		if changed {
			a.bumpLocked(m)
			a.updates++
		}
		prior := customModelFixtureReceipt{UUID: id, InputHash: hash, Token: m.token()}
		a.updateReceipts[key] = prior
		committed := a.updateReceiptLocked(key, prior)
		if a.changeAfterUpdate {
			a.changeAfterUpdate = false
			m.Credential = "later-credential-fixture"
			a.bumpLocked(m)
		}
		if a.dropUpdate {
			a.dropUpdate = false
			if a.blockAfterDrop {
				a.updateReceiptFail = 503
			}
			fail(502)
			return
		}
		respond(200, committed, map[string]any{"idempotency_replayed": false})
		return
	}
	a.unexpected = append(a.unexpected, r.Method+" "+r.URL.RequestURI())
	fail(405)
}

func (a *customModelTestAPI) creationReceiptLocked(key string, prior customModelFixtureReceipt) any {
	if prior.Stateful {
		return map[string]any{"idempotency_key": key, "custom_model_uuid": prior.UUID, "resource_exists": a.models[prior.UUID] != nil, "initial_change_token": prior.Token}
	}
	return client.CustomModelCreationReceipt{IdempotencyKey: key, CustomModelUUID: prior.UUID, ResourceExists: a.models[prior.UUID] != nil}
}

func (a *customModelTestAPI) updateReceiptLocked(key string, prior customModelFixtureReceipt) client.CustomModelUpdateReceipt {
	m := a.models[prior.UUID]
	match := m != nil && m.token() == prior.Token
	var result *bool
	if m == nil || !a.unknownMatch {
		result = &match
	}
	return client.CustomModelUpdateReceipt{IdempotencyKey: key, CustomModelUUID: prior.UUID, ResourceExists: m != nil, ResultMatchesCurrentState: result}
}
