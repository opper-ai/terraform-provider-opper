package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

const projectAccessTestOrg = "11111111-1111-4111-8111-111111111111"
const projectAccessOtherOrg = "11111111-1111-4111-8111-111111111112"
const projectAccessTestProject = "22222222-2222-4222-8222-222222222222"
const projectAccessOtherProject = "22222222-2222-4222-8222-222222222223"
const projectAccessTestUser = "33333333-3333-4333-8333-333333333333"

type projectAccessFixture struct {
	OrganizationUUID string   `json:"organization_uuid"`
	ProjectUUID      string   `json:"project_uuid"`
	Mode             string   `json:"mode"`
	RoleKeys         []string `json:"role_keys"`
	UserUUIDs        []string `json:"user_uuids"`
	Revision         int64    `json:"revision"`
	ChangeToken      string   `json:"change_token"`
}

type projectAccessTestAPI struct {
	mu                                  sync.Mutex
	server                              *httptest.Server
	projects                            map[string]*projectAccessFixture
	requests                            []string
	puts, writes, membershipIncarnation int
	readFailure, putFailure             int
	failAfterWrite, malformedAfterWrite bool
}

func newProjectAccessTestAPI(t *testing.T) *projectAccessTestAPI {
	t.Helper()
	a := &projectAccessTestAPI{projects: map[string]*projectAccessFixture{}}
	for _, id := range []string{projectAccessTestProject, projectAccessOtherProject} {
		p := &projectAccessFixture{OrganizationUUID: projectAccessTestOrg, ProjectUUID: id, Mode: "organization", RoleKeys: []string{}, UserUUIDs: []string{}}
		a.projects[id] = p
		a.retoken(p)
	}
	a.server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.server.Close)
	return a
}

// Tokens intentionally include a membership incarnation independently of the
// revision, matching a grant cascade in the real backend.
func (a *projectAccessTestAPI) retoken(p *projectAccessFixture) {
	p.ChangeToken = ""
	b, _ := json.Marshal(struct {
		Policy     projectAccessFixture
		Membership int
	}{*p, a.membershipIncarnation})
	h := sha256.Sum256(b)
	p.ChangeToken = "pav1_" + base64.RawURLEncoding.EncodeToString(h[:])
}

func (a *projectAccessTestAPI) edit(id, mode string, roles, users []string, bumpRevision bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[id]
	p.Mode, p.RoleKeys, p.UserUUIDs = mode, append([]string{}, roles...), append([]string{}, users...)
	sort.Strings(p.RoleKeys)
	sort.Strings(p.UserUUIDs)
	if bumpRevision {
		p.Revision++
	} else {
		a.membershipIncarnation++
	}
	a.retoken(p)
}

func (a *projectAccessTestAPI) counts() (puts, writes int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.puts, a.writes
}

func (a *projectAccessTestAPI) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "management" || parts[1] != "v1" || parts[2] != "projects" || parts[4] != "access" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	p, ok := a.projects[parts[3]]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		if a.readFailure != 0 {
			w.WriteHeader(a.readFailure)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": p})
		return
	}
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a.puts++
	if a.putFailure != 0 {
		w.WriteHeader(a.putFailure)
		return
	}
	var intent struct {
		Mode                string   `json:"mode"`
		RoleKeys            []string `json:"role_keys"`
		UserUUIDs           []string `json:"user_uuids"`
		ExpectedChangeToken string   `json:"expected_change_token"`
	}
	if json.NewDecoder(r.Body).Decode(&intent) != nil || intent.RoleKeys == nil || intent.UserUUIDs == nil || intent.Mode != "restricted" && intent.Mode != "organization" || intent.Mode == "organization" && len(intent.RoleKeys)+len(intent.UserUUIDs) != 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if intent.ExpectedChangeToken != p.ChangeToken {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if p.Mode != intent.Mode || !reflect.DeepEqual(p.RoleKeys, intent.RoleKeys) || !reflect.DeepEqual(p.UserUUIDs, intent.UserUUIDs) {
		p.Mode, p.RoleKeys, p.UserUUIDs = intent.Mode, intent.RoleKeys, intent.UserUUIDs
		p.Revision++
		a.writes++
		a.retoken(p)
	}
	if a.failAfterWrite {
		a.failAfterWrite = false
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if a.malformedAfterWrite {
		a.malformedAfterWrite = false
		_, _ = fmt.Fprint(w, `{"data":`)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": p})
}
