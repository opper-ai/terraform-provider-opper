package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const memberTestAliceUUID = "11111111-1111-4111-8111-111111111111"
const memberTestBobUUID = "22222222-2222-4222-8222-222222222222"

// This fixture deliberately exposes only the collection GET. Member lookup
// must not mutate membership, invite anyone, or call user/session endpoints.
type memberTestRecord struct {
	UUID      string  `json:"uuid"`
	Email     string  `json:"email"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	IsOwner   bool    `json:"is_owner"`
	CreatedAt *string `json:"created_at,omitempty"`
}

type memberTestAPI struct {
	mu         sync.Mutex
	server     *httptest.Server
	members    []memberTestRecord
	reads      int
	failure    int
	unexpected []string
}

func newMemberTestAPI(t *testing.T) *memberTestAPI {
	t.Helper()
	created := "2026-11-03T09:00:00Z"
	api := &memberTestAPI{members: []memberTestRecord{
		{UUID: memberTestBobUUID, Email: "", Name: "", Role: "", IsOwner: false},
		{UUID: memberTestAliceUUID, Email: "alice@example.com", Name: "Alice", Role: "admin", IsOwner: true, CreatedAt: &created},
	}}
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(api.server.Close)
	t.Cleanup(func() {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.unexpected) != 0 {
			t.Errorf("lookup attempted unexpected operations: %v", api.unexpected)
		}
	})
	return api
}

func (api *memberTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fail := func(code int, message string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
	}
	if r.Method != http.MethodGet || r.URL.Path != "/management/v1/members" || r.URL.RawQuery != "" {
		api.unexpected = append(api.unexpected, r.Method+" "+r.URL.RequestURI())
		fail(http.StatusMethodNotAllowed, "only GET /management/v1/members is allowed")
		return
	}
	api.reads++
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		fail(http.StatusUnauthorized, "unauthenticated")
		return
	}
	if api.failure != 0 {
		fail(api.failure, "members unavailable")
		return
	}
	members := append([]memberTestRecord{}, api.members...)
	// A stable Terraform list must not depend on API iteration order.
	if api.reads%2 == 0 {
		for i, j := 0, len(members)-1; i < j; i, j = i+1, j-1 {
			members[i], members[j] = members[j], members[i]
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"total_count": len(members)}, "data": members})
}

func (api *memberTestAPI) setMembers(members []memberTestRecord) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.members = append([]memberTestRecord{}, members...)
}
