package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoleLocalValidationNeverSendsMutation(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer s.Close()
	c, err := New(s.URL, "op-mak-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateRole(context.Background(), RoleCreateInput{DisplayName: "\u00a0", Permissions: []string{"usage:read"}, Priority: 150, IdempotencyKey: "create-key"})
	if !IsLocalValidationError(err) || calls != 0 {
		t.Fatalf("expected local refusal without request, got err=%v calls=%d", err, calls)
	}
}

func TestRoleStrictResponseContract(t *testing.T) {
	label := "Readers"
	role := Role{OrganizationUUID: "11111111-1111-4111-8111-111111111111", Name: "custom_11111111111111111111111111111111", DisplayName: &label, Permissions: []string{"usage:read"}, EffectivePermissions: []string{"usage:read"}, Priority: 150, Custom: true, ChangeToken: "orv1_" + strings.Repeat("A", 43)}
	b, _ := json.Marshal(role)
	valid := string(b)
	cases := map[string]string{"valid": valid, "unknown": strings.Replace(valid, `"priority":150`, `"priority":150,"credential":"secret"`, 1), "duplicate": strings.Replace(valid, `"priority":150`, `"priority":150,"priority":150`, 1), "missing": strings.Replace(valid, `"priority":150,`, "", 1), "null-priority": strings.Replace(valid, `"priority":150`, `"priority":null`, 1), "fractional-priority": strings.Replace(valid, `"priority":150`, `"priority":1.5`, 1), "string-priority": strings.Replace(valid, `"priority":150`, `"priority":"150"`, 1), "null-boolean": strings.Replace(valid, `"custom":true`, `"custom":null`, 1), "foreign-type": strings.Replace(valid, `"custom":true`, `"custom":false`, 1), "bad-token": strings.Replace(valid, "orv1_", "omrv1_", 1), "null-permissions": strings.Replace(valid, `"permissions":["usage:read"]`, `"permissions":null`, 1)}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"meta":{},"data":%s}`, body) }))
			defer s.Close()
			c, _ := New(s.URL, "op-mak-test", "test")
			v, e := c.GetRole(context.Background(), role.Name)
			if label == "valid" {
				if e != nil || v.Name != role.Name {
					t.Fatalf("valid role refused: %v", e)
				}
			} else if e == nil || IsLocalValidationError(e) {
				t.Fatalf("invalid response must be uncertain, got %v", e)
			}
		})
	}
}

func TestRoleReadsPreserveLegacyMetadata(t *testing.T) {
	const org = "11111111-1111-4111-8111-111111111111"
	const name = "custom_11111111111111111111111111111111"
	for _, which := range []string{"nullable label", "empty grants", "legacy zero priority", "unknown grants", "reserved legacy label"} {
		t.Run(which, func(t *testing.T) {
			label := "Legacy role"
			legacy := Role{OrganizationUUID: org, Name: name, DisplayName: &label, Permissions: []string{"usage:read"}, EffectivePermissions: []string{"usage:read"}, Priority: 150, Custom: true, ChangeToken: "orv1_" + strings.Repeat("A", 43)}
			switch which {
			case "nullable label":
				legacy.DisplayName = nil
			case "empty grants":
				legacy.Permissions = []string{}
				legacy.EffectivePermissions = []string{}
			case "legacy zero priority":
				legacy.Priority = 0
			case "unknown grants":
				legacy.Permissions = []string{"future:capability", "usage:read"}
			case "reserved legacy label":
				label = "Admin"
			}
			writes := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(500)
					return
				}
				var data any = legacy
				if r.URL.Path == "/management/v1/roles" {
					data = RoleCatalog{Roles: []Role{legacy}, Permissions: []string{"usage:read"}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": data})
			}))
			defer s.Close()
			c, _ := New(s.URL, "op-mak-test", "test")
			got, err := c.GetRole(context.Background(), name)
			if err != nil {
				t.Fatalf("contract-valid legacy role refused: %v", err)
			}
			catalog, err := c.GetRoleCatalog(context.Background())
			if err != nil || len(catalog.Roles) != 1 {
				t.Fatalf("legacy role poisoned organization catalog: %v", err)
			}
			for _, v := range []Role{got, catalog.Roles[0]} {
				encoded, _ := json.Marshal(v)
				expected, _ := json.Marshal(legacy)
				if string(encoded) != string(expected) {
					t.Fatal("read normalized or discarded legacy metadata")
				}
			}
			// Reading legacy rows does not make their unsupported definition a
			// valid write intent or enter uncertain-operation recovery.
			_, err = c.CreateRole(context.Background(), RoleCreateInput{DisplayName: "", Permissions: []string{}, Priority: 0, IdempotencyKey: "legacy-write"})
			if !IsLocalValidationError(err) || writes != 0 {
				t.Fatal("read compatibility weakened mutation validation")
			}
		})
	}
}

func TestRoleMutationPreconditionsAndReceiptProof(t *testing.T) {
	calls := 0
	key := "operation-test"
	name := "custom_11111111111111111111111111111111"
	roleToken := "orv1_" + strings.Repeat("A", 43)
	memberToken := "omrv1_" + strings.Repeat("B", 43)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"meta":{},"data":{"idempotency_key":%q,"resource_id":%q,"resource_exists":false,"result_change_token":%q,"result_matches_current_state":false}}`, key, name, roleToken)
			return
		}
		w.WriteHeader(409)
	}))
	defer s.Close()
	c, _ := New(s.URL, "op-mak-test", "test")
	if e := c.DeleteRole(context.Background(), "viewer", roleToken); !IsLocalValidationError(e) {
		t.Fatal("built-in deletion was not local refusal")
	}
	if _, e := c.SetMemberRole(context.Background(), "11111111-1111-4111-8111-111111111111", MemberRoleInput{Role: "owner", ExpectedChangeToken: memberToken, ExpectedRoleChangeToken: roleToken, IdempotencyKey: key}); !IsLocalValidationError(e) {
		t.Fatal("owner assignment was not local refusal")
	}
	if calls != 0 {
		t.Fatal("local validation sent HTTP")
	}
	e := c.DeleteRole(context.Background(), name, roleToken)
	if e == nil || IsLocalValidationError(e) || calls != 1 {
		t.Fatal("remote conflict was misclassified")
	}
	v, e := c.GetRoleOperation(context.Background(), "create", key)
	if e != nil || v.ResourceExists || v.ResultChangeToken != roleToken || v.ResultMatchesCurrentState == nil || *v.ResultMatchesCurrentState {
		t.Fatalf("original deleted receipt proof not preserved: %+v %v", v, e)
	}
}

func TestRoleMutationResponseCannotChangeIntent(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	token := "omrv1_" + strings.Repeat("A", 43)
	roleToken := "orv1_" + strings.Repeat("B", 43)
	stored := "admin"
	snapshot := MemberRole{OrganizationUUID: id, MemberUUID: id, StoredRole: &stored, Role: "admin", ChangeToken: token}
	match := true
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"idempotency_replayed": false, "operation": RoleReceipt{IdempotencyKey: "command", ResourceID: id, ResourceExists: true, ResultChangeToken: token, ResultMatchesCurrentState: &match}}, "data": snapshot})
	}))
	defer s.Close()
	c, _ := New(s.URL, "op-mak-test", "test")
	_, e := c.SetMemberRole(context.Background(), id, MemberRoleInput{Role: "viewer", ExpectedChangeToken: token, ExpectedRoleChangeToken: roleToken, IdempotencyKey: "command"})
	if e == nil || IsLocalValidationError(e) {
		t.Fatal("server response describing a different assignment must remain uncertain")
	}
}
