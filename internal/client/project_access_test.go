package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const projectAccessTestProject = "22222222-2222-4222-8222-222222222222"
const projectAccessTestOrg = "11111111-1111-4111-8111-111111111111"
const projectAccessTestUser = "33333333-3333-4333-8333-333333333333"

func projectAccessTestSnapshot() ProjectAccessPolicy {
	return ProjectAccessPolicy{OrganizationUUID: projectAccessTestOrg, ProjectUUID: projectAccessTestProject, Mode: "restricted", RoleKeys: []string{"admin", "viewer"}, UserUUIDs: []string{projectAccessTestUser}, Revision: 3, ChangeToken: "pav1_" + strings.Repeat("A", 43)}
}

func TestProjectAccessClientReadAndConditionalReplacement(t *testing.T) {
	current := projectAccessTestSnapshot()
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.URL.Path != "/management/v1/projects/"+projectAccessTestProject+"/access" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected project-access request")
		}
		if r.Method == http.MethodPut {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(body, &fields) != nil || len(fields) != 4 {
				t.Errorf("replacement did not contain exactly four fields")
			}
			var input ProjectAccessInput
			if json.Unmarshal(body, &input) != nil || input.ExpectedChangeToken != current.ChangeToken || !reflect.DeepEqual(input.RoleKeys, []string{"admin", "viewer"}) || !reflect.DeepEqual(input.UserUUIDs, []string{projectAccessTestUser}) {
				t.Errorf("replacement changed the reviewed intent: %s", body)
			}
			current.ChangeToken = "pav1_" + strings.Repeat("B", 43)
			current.Revision++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": current})
	}))
	defer server.Close()
	c, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.GetProjectAccess(context.Background(), projectAccessTestProject)
	if err != nil || !reflect.DeepEqual(before, current) {
		t.Fatalf("valid snapshot refused: %v", err)
	}
	input := ProjectAccessInput{Mode: "restricted", RoleKeys: []string{"viewer", "admin", "viewer"}, UserUUIDs: []string{projectAccessTestUser, projectAccessTestUser}, ExpectedChangeToken: before.ChangeToken}
	after, err := c.ReplaceProjectAccess(context.Background(), projectAccessTestProject, input)
	if err != nil || after.Revision != 4 || after.ChangeToken == before.ChangeToken {
		t.Fatalf("replacement failed: %v", err)
	}
	if !reflect.DeepEqual(methods, []string{"GET", "PUT"}) || len(input.RoleKeys) != 3 || input.RoleKeys[0] != "viewer" {
		t.Fatalf("client retried a request or mutated caller-owned slices")
	}
}

func TestProjectAccessClientEncodesExplicitEmptyArrays(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"role_keys":[]`) || !strings.Contains(string(body), `"user_uuids":[]`) {
			t.Errorf("empty grants must be explicit arrays: %s", body)
		}
		v := projectAccessTestSnapshot()
		v.Mode, v.RoleKeys, v.UserUUIDs = "organization", []string{}, []string{}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": v})
	}))
	defer server.Close()
	c, _ := New(server.URL, "test-key", "test")
	if _, err := c.ReplaceProjectAccess(context.Background(), projectAccessTestProject, ProjectAccessInput{Mode: "organization", ExpectedChangeToken: projectAccessTestSnapshot().ChangeToken}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectAccessClientRejectsInvalidInputsWithoutRequests(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	c, _ := New(server.URL, "test-key", "test")
	valid := ProjectAccessInput{Mode: "restricted", ExpectedChangeToken: projectAccessTestSnapshot().ChangeToken}
	for _, id := range []string{"", "../projects", strings.ToUpper(projectAccessTestProject[:8]) + "-ABCD-4222-8222-222222222222", "00000000-0000-0000-0000-000000000000"} {
		if _, err := c.GetProjectAccess(context.Background(), id); !IsLocalValidationError(err) {
			t.Errorf("invalid read identity not rejected locally: %v", err)
		}
		if _, err := c.ReplaceProjectAccess(context.Background(), id, valid); !IsLocalValidationError(err) {
			t.Errorf("invalid write identity not rejected locally: %v", err)
		}
	}
	inputs := []ProjectAccessInput{
		{}, {Mode: "Restricted", ExpectedChangeToken: valid.ExpectedChangeToken},
		{Mode: "restricted", ExpectedChangeToken: "not-a-token"},
		{Mode: "organization", RoleKeys: []string{"viewer"}, ExpectedChangeToken: valid.ExpectedChangeToken},
		{Mode: "restricted", RoleKeys: []string{"owner"}, ExpectedChangeToken: valid.ExpectedChangeToken},
		{Mode: "restricted", UserUUIDs: []string{"00000000-0000-0000-0000-000000000000"}, ExpectedChangeToken: valid.ExpectedChangeToken},
		{Mode: "restricted", RoleKeys: make([]string, 501), ExpectedChangeToken: valid.ExpectedChangeToken},
		{Mode: "restricted", UserUUIDs: make([]string, 501), ExpectedChangeToken: valid.ExpectedChangeToken},
	}
	large := ProjectAccessInput{Mode: "restricted", ExpectedChangeToken: valid.ExpectedChangeToken}
	for i := 0; i < 500; i++ {
		large.RoleKeys = append(large.RoleKeys, fmt.Sprintf("custom_%032x", i))
		large.UserUUIDs = append(large.UserUUIDs, fmt.Sprintf("%08x-3333-4333-8333-333333333333", i+1))
	}
	inputs = append(inputs, large)
	for i, input := range inputs {
		if _, err := c.ReplaceProjectAccess(context.Background(), projectAccessTestProject, input); !IsLocalValidationError(err) {
			t.Errorf("case %d was not rejected locally: %v", i, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid input reached HTTP: %d", calls)
	}
}

func TestProjectAccessClientRejectsInvalidSnapshots(t *testing.T) {
	b, _ := json.Marshal(projectAccessTestSnapshot())
	valid := string(b)
	cases := map[string]string{
		"valid":                    valid,
		"missing revision":         strings.Replace(valid, `"revision":3,`, "", 1),
		"null revision":            strings.Replace(valid, `"revision":3`, `"revision":null`, 1),
		"negative revision":        strings.Replace(valid, `"revision":3`, `"revision":-1`, 1),
		"fractional revision":      strings.Replace(valid, `"revision":3`, `"revision":1.5`, 1),
		"duplicate mode":           strings.Replace(valid, `"mode":"restricted"`, `"mode":"restricted","mode":"organization"`, 1),
		"unknown field":            strings.Replace(valid, `"revision":3`, `"revision":3,"private":"secret"`, 1),
		"wrong project":            strings.Replace(valid, projectAccessTestProject, projectAccessTestUser, 1),
		"zero organization":        strings.Replace(valid, projectAccessTestOrg, "00000000-0000-0000-0000-000000000000", 1),
		"wrong token":              strings.Replace(valid, "pav1_", "orv1_", 1),
		"null roles":               strings.Replace(valid, `["admin","viewer"]`, "null", 1),
		"null user":                strings.Replace(valid, `"`+projectAccessTestUser+`"`, "null", 1),
		"unsorted roles":           strings.Replace(valid, `["admin","viewer"]`, `["viewer","admin"]`, 1),
		"duplicate roles":          strings.Replace(valid, `["admin","viewer"]`, `["viewer","viewer"]`, 1),
		"unsupported role":         strings.Replace(valid, `"admin"`, `"owner"`, 1),
		"organization with grants": strings.Replace(valid, `"restricted"`, `"organization"`, 1),
		"missing array":            strings.Replace(valid, `"role_keys":["admin","viewer"],`, "", 1),
		"trailing JSON":            valid + `{}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = fmt.Fprintf(w, `{"meta":{},"data":%s}`, body)
			}))
			defer server.Close()
			c, _ := New(server.URL, "test-key", "test")
			for _, method := range []string{"GET", "PUT"} {
				var err error
				if method == "GET" {
					_, err = c.GetProjectAccess(context.Background(), projectAccessTestProject)
				} else {
					_, err = c.ReplaceProjectAccess(context.Background(), projectAccessTestProject, ProjectAccessInput{Mode: "restricted", ExpectedChangeToken: projectAccessTestSnapshot().ChangeToken})
				}
				if label == "valid" && err != nil {
					t.Fatalf("valid response refused: %v", err)
				}
				if label != "valid" && (err == nil || IsLocalValidationError(err) || strings.Contains(err.Error(), "secret")) {
					t.Fatalf("invalid response must remain a sanitized uncertain result: %v", err)
				}
			}
			if calls != 2 {
				t.Fatalf("response triggered automatic retry or recovery: %d calls", calls)
			}
		})
	}
}

func TestProjectAccessClientHTTPFailuresNeverRetry(t *testing.T) {
	for _, code := range []int{401, 403, 404, 409, 429, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"error":"secret response"}`))
			}))
			defer server.Close()
			c, _ := New(server.URL, "test-key", "test")
			_, err := c.ReplaceProjectAccess(context.Background(), projectAccessTestProject, ProjectAccessInput{Mode: "restricted", ExpectedChangeToken: projectAccessTestSnapshot().ChangeToken})
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) || strings.Contains(err.Error(), "secret") || calls != 1 {
				t.Fatalf("failure was not preserved without retry: %v, calls=%d", err, calls)
			}
		})
	}
}
