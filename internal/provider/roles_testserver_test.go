package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

const rolesTestOrg = "11111111-1111-4111-8111-111111111111"
const rolesTestMember = "22222222-2222-4222-8222-222222222222"

type roleFixtureReceipt struct{ Kind, ID, Token, Role, RoleToken string }
type rolesTestAPI struct {
	mu                                                           sync.Mutex
	server                                                       *httptest.Server
	roles                                                        map[string]client.Role
	member                                                       client.MemberRole
	receipts                                                     map[string]roleFixtureReceipt
	sequence                                                     int
	creates, roleUpdates, assignments, deletes                   int
	requests                                                     []string
	dropRoleWrite, dropMemberWrite, blockReceipts, missingMember bool
	readFailure                                                  int
}

func rolesTestToken(prefix, value string) string {
	h := sha256.Sum256([]byte(value))
	return prefix + base64.RawURLEncoding.EncodeToString(h[:])
}
func newRolesTestAPI(t *testing.T) *rolesTestAPI {
	t.Helper()
	a := &rolesTestAPI{roles: map[string]client.Role{}, receipts: map[string]roleFixtureReceipt{}}
	for _, name := range []string{"admin", "developer", "viewer"} {
		label := name
		grants := []string{"usage:read"}
		if name != "viewer" {
			grants = append(grants, "projects:write")
			sort.Strings(grants)
		}
		a.roles[name] = client.Role{OrganizationUUID: rolesTestOrg, Name: name, DisplayName: &label, Permissions: grants, EffectivePermissions: grants, Priority: map[string]int64{"admin": 300, "developer": 200, "viewer": 100}[name], ChangeToken: rolesTestToken("orv1_", name)}
	}
	stored := "developer"
	a.member = client.MemberRole{OrganizationUUID: rolesTestOrg, MemberUUID: rolesTestMember, StoredRole: &stored, Role: stored, ChangeToken: rolesTestToken("omrv1_", "membership-insertion-1")}
	a.server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.server.Close)
	return a
}
func (a *rolesTestAPI) nextToken(prefix string) string {
	a.sequence++
	return rolesTestToken(prefix, fmt.Sprint(a.sequence))
}
func (a *rolesTestAPI) receipt(key string, r roleFixtureReceipt) client.RoleReceipt {
	exists, match := false, false
	if r.Kind == "member" {
		exists = !a.missingMember
		role, ok := a.roles[r.Role]
		match = exists && a.member.ChangeToken == r.Token && ok && role.ChangeToken == r.RoleToken
	} else {
		v, ok := a.roles[r.ID]
		exists = ok
		match = ok && v.ChangeToken == r.Token
	}
	return client.RoleReceipt{IdempotencyKey: key, ResourceID: r.ID, ResourceExists: exists, ResultChangeToken: r.Token, ResultMatchesCurrentState: &match}
}
func (a *rolesTestAPI) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	send := func(code int, meta, data any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta, "data": data})
	}
	empty := map[string]any{}
	base := "/management/v1/"
	p := strings.TrimPrefix(r.URL.Path, base)
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		w.WriteHeader(401)
		return
	}
	if r.Method == http.MethodGet && a.readFailure != 0 {
		w.WriteHeader(a.readFailure)
		return
	}
	if strings.HasPrefix(p, "role-operations/") || strings.HasPrefix(p, "member-role-operations/") {
		if a.blockReceipts {
			w.WriteHeader(503)
			return
		}
		parts := strings.Split(p, "/")
		key := parts[len(parts)-1]
		receipt, ok := a.receipts[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		send(200, empty, a.receipt(key, receipt))
		return
	}
	if p == "roles" && r.Method == http.MethodGet {
		v := client.RoleCatalog{Roles: []client.Role{}, Permissions: []string{"projects:write", "usage:read"}}
		for _, role := range a.roles {
			v.Roles = append(v.Roles, role)
		}
		send(200, empty, v)
		return
	}
	if p == "roles" && r.Method == http.MethodPost {
		var input client.RoleCreateInput
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			w.WriteHeader(400)
			return
		}
		if receipt, ok := a.receipts[input.IdempotencyKey]; ok {
			role, exists := a.roles[receipt.ID]
			if !exists {
				w.WriteHeader(409)
				return
			}
			send(200, map[string]any{"idempotency_replayed": true, "operation": a.receipt(input.IdempotencyKey, receipt)}, role)
			return
		}
		a.creates++
		name := fmt.Sprintf("custom_%032x", a.creates)
		label := input.DisplayName
		v := client.Role{OrganizationUUID: rolesTestOrg, Name: name, DisplayName: &label, Permissions: input.Permissions, EffectivePermissions: input.Permissions, Priority: input.Priority, Custom: true, ChangeToken: a.nextToken("orv1_")}
		a.roles[name] = v
		receipt := roleFixtureReceipt{Kind: "role", ID: name, Token: v.ChangeToken}
		a.receipts[input.IdempotencyKey] = receipt
		if a.dropRoleWrite {
			a.dropRoleWrite = false
			a.blockReceipts = true
			w.WriteHeader(500)
			return
		}
		send(201, map[string]any{"idempotency_replayed": false, "operation": a.receipt(input.IdempotencyKey, receipt)}, v)
		return
	}
	if strings.HasPrefix(p, "roles/") {
		name := strings.TrimPrefix(p, "roles/")
		v, exists := a.roles[name]
		if r.Method == http.MethodGet {
			if !exists {
				w.WriteHeader(404)
				return
			}
			send(200, empty, v)
			return
		}
		if r.Method == http.MethodDelete {
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			if !exists {
				w.WriteHeader(204)
				return
			}
			if !v.Custom || input["expected_change_token"] != v.ChangeToken || a.member.Role == name {
				w.WriteHeader(409)
				return
			}
			delete(a.roles, name)
			a.deletes++
			w.WriteHeader(204)
			return
		}
		if r.Method == http.MethodPut {
			var input client.RoleUpdateInput
			_ = json.NewDecoder(r.Body).Decode(&input)
			if !exists || input.ExpectedChangeToken != v.ChangeToken || !v.Custom {
				w.WriteHeader(409)
				return
			}
			a.roleUpdates++
			label := input.DisplayName
			v.DisplayName = &label
			v.Permissions = input.Permissions
			v.EffectivePermissions = input.Permissions
			v.Priority = input.Priority
			v.ChangeToken = a.nextToken("orv1_")
			a.roles[name] = v
			receipt := roleFixtureReceipt{Kind: "role", ID: name, Token: v.ChangeToken}
			a.receipts[input.IdempotencyKey] = receipt
			if a.dropRoleWrite {
				a.dropRoleWrite = false
				a.blockReceipts = true
				w.WriteHeader(500)
				return
			}
			send(200, map[string]any{"idempotency_replayed": false, "operation": a.receipt(input.IdempotencyKey, receipt)}, v)
			return
		}
	}
	if p == "members/"+rolesTestMember+"/role" {
		if a.missingMember {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodGet {
			send(200, empty, a.member)
			return
		}
		if r.Method == http.MethodPut {
			var input client.MemberRoleInput
			_ = json.NewDecoder(r.Body).Decode(&input)
			dest, ok := a.roles[input.Role]
			if !ok || a.member.Owner || a.member.Blocked || a.member.DirectoryManaged || a.member.ChangeToken != input.ExpectedChangeToken || dest.ChangeToken != input.ExpectedRoleChangeToken {
				w.WriteHeader(409)
				return
			}
			a.assignments++
			if a.member.Role != input.Role {
				stored := input.Role
				a.member.Role = input.Role
				a.member.StoredRole = &stored
				a.member.ChangeToken = a.nextToken("omrv1_")
			}
			receipt := roleFixtureReceipt{Kind: "member", ID: rolesTestMember, Token: a.member.ChangeToken, Role: input.Role, RoleToken: input.ExpectedRoleChangeToken}
			a.receipts[input.IdempotencyKey] = receipt
			if a.dropMemberWrite {
				a.dropMemberWrite = false
				a.blockReceipts = true
				w.WriteHeader(500)
				return
			}
			send(200, map[string]any{"idempotency_replayed": false, "operation": a.receipt(input.IdempotencyKey, receipt)}, a.member)
			return
		}
	}
	w.WriteHeader(404)
}
