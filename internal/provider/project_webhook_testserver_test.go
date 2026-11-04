package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const webhookTestProjectID = "11111111-1111-4111-8111-111111111111"
const webhookTestOtherProjectID = "22222222-2222-4222-8222-222222222222"

// Only the non-secret overview and URL mutation exist in this independent
// fixture. Unexpected project, signing-secret, or delivery operations fail.
type webhookTestOverview struct {
	URL                      *string `json:"url"`
	URLUpdatedAt             *string `json:"url_updated_at"`
	SecretCreatedAt          *string `json:"secret_created_at"`
	PreviousSecretValidUntil *string `json:"previous_secret_valid_until"`
}

type projectWebhookTestAPI struct {
	mu                       sync.Mutex
	server                   *httptest.Server
	projects                 map[string]webhookTestOverview
	reads, patches, clears   int
	getFailure, patchFailure int
	unexpected               []string
	// Change remote state immediately after returning one successful overview,
	// modelling a concurrent edit between Terraform's refresh and destroy.
	replaceAfterNextRead       *string
	malformedNextPatchResponse bool
}

func newProjectWebhookTestAPI(t *testing.T) *projectWebhookTestAPI {
	t.Helper()
	secret := "2026-11-03T09:00:00Z"
	overlap := "2026-11-04T09:00:00Z"
	api := &projectWebhookTestAPI{projects: map[string]webhookTestOverview{
		webhookTestProjectID:      {SecretCreatedAt: &secret, PreviousSecretValidUntil: &overlap},
		webhookTestOtherProjectID: {},
	}}
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(api.server.Close)
	t.Cleanup(func() {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.unexpected) != 0 {
			t.Errorf("unexpected API operations: %v", api.unexpected)
		}
	})
	return api
}

func (api *projectWebhookTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	failure := func(code int, message string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
	}
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		failure(http.StatusUnauthorized, "unauthenticated")
		return
	}
	const prefix = "/management/v1/projects/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/webhook") {
		api.unexpected = append(api.unexpected, r.Method+" "+r.URL.Path)
		failure(http.StatusNotFound, "only webhook overview is supported")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/webhook")
	overview, found := api.projects[id]
	if !found {
		failure(http.StatusNotFound, "project not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		api.reads++
		if api.getFailure != 0 {
			failure(api.getFailure, "webhook overview unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": overview})
		if api.replaceAfterNextRead != nil {
			overview.URL = api.replaceAfterNextRead
			api.projects[id] = overview
			api.replaceAfterNextRead = nil
		}
	case http.MethodPatch:
		if api.patchFailure != 0 {
			failure(api.patchFailure, "webhook update unavailable")
			return
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			failure(http.StatusBadRequest, "invalid body")
			return
		}
		raw, ok := body["url"]
		if len(body) != 1 || !ok {
			api.unexpected = append(api.unexpected, fmt.Sprintf("PATCH fields: %v", body))
			failure(http.StatusBadRequest, "only url may be changed")
			return
		}
		var url *string
		if err := json.Unmarshal(raw, &url); err != nil {
			failure(http.StatusBadRequest, "invalid url")
			return
		}
		if url != nil && !strings.HasPrefix(*url, "https://") {
			failure(http.StatusBadRequest, "url must use https")
			return
		}
		updated := "2026-11-04T10:00:00Z"
		overview.URL, overview.URLUpdatedAt = url, &updated
		// The real SecretStore.SetDefaultURL initializes a secret on first save.
		if overview.SecretCreatedAt == nil {
			created := "2026-11-04T10:00:00Z"
			overview.SecretCreatedAt = &created
		}
		api.projects[id] = overview
		api.patches++
		if url == nil {
			api.clears++
		}
		if api.malformedNextPatchResponse {
			api.malformedNextPatchResponse = false
			_, _ = w.Write([]byte(`{"meta":{},"data":`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": overview})
	default:
		api.unexpected = append(api.unexpected, r.Method+" "+r.URL.Path)
		failure(http.StatusMethodNotAllowed, "only GET and PATCH are supported")
	}
}

func (api *projectWebhookTestAPI) setURL(id string, url *string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	overview := api.projects[id]
	overview.URL = url
	api.projects[id] = overview
}

func webhookString(value string) *string { return &value }
