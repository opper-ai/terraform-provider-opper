package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const memberFirstUUID = "11111111-1111-4111-8111-111111111111"
const memberSecondUUID = "22222222-2222-4222-8222-222222222222"
const memberFirstJSON = `{"uuid":"11111111-1111-4111-8111-111111111111","email":"","name":"","role":"","is_owner":false}`

func TestListMembersContract(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/management/v1/members" {
			t.Error("member lookup accessed an unexpected method or endpoint")
		}
		if r.Header.Get("Authorization") != "Bearer management-secret" || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "terraform-provider-opper/test" {
			t.Error("missing member read request headers")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			t.Error("member lookup sent a request body")
		}
		fmt.Fprint(w, `{"meta":{"total_count":2},"data":[{"uuid":"22222222-2222-4222-8222-222222222222","email":"member@example.test","name":"Member","role":"custom-role","is_owner":true,"created_at":"2026-11-04T10:00:00Z"},`+memberFirstJSON+`]}`)
	}))
	defer server.Close()
	c, err := New(server.URL, "management-secret", "test")
	if err != nil {
		t.Fatal(err)
	}
	members, err := c.ListMembers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	createdAt := "2026-11-04T10:00:00Z"
	want := []Member{
		{UUID: memberFirstUUID},
		{UUID: memberSecondUUID, Email: "member@example.test", Name: "Member", Role: "custom-role", IsOwner: true, CreatedAt: &createdAt},
	}
	if !reflect.DeepEqual(members, want) {
		t.Fatal("member list lost metadata or was not sorted by UUID")
	}
	if calls.Load() != 1 {
		t.Fatal("member list did not use exactly one read")
	}
}

func TestListMembersEmptyAndNullableCreatedAt(t *testing.T) {
	for _, body := range []string{
		`{"meta":{"total_count":0},"data":[]}`,
		`{"meta":{"total_count":1},"data":[{"uuid":"11111111-1111-4111-8111-111111111111","email":"","name":"","role":"","is_owner":false,"created_at":null}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			members, err := c.ListMembers(context.Background())
			if err != nil || members == nil || len(members) > 0 && members[0].CreatedAt != nil {
				t.Fatal("valid empty list or nullable account timestamp was rejected")
			}
		})
	}
}

func TestListMembersRejectsMalformedOrIncompleteResponses(t *testing.T) {
	bodies := []string{
		``, `{`, `null`, `{}`, `[]`, `{"meta":{"total_count":0}}`,
		`{"meta":{"total_count":0},"data":null}`, `{"meta":{"total_count":0},"data":{}}`,
		`{"data":[]}`, `{"meta":null,"data":[]}`, `{"meta":{},"data":[]}`,
		`{"meta":{"total_count":null},"data":[]}`, `{"meta":{"total_count":"0"},"data":[]}`,
		`{"meta":{"total_count":-1},"data":[]}`, `{"meta":{"total_count":1},"data":[]}`,
		`{"meta":{"total_count":0.5},"data":[]}`, `{"meta":{"total_count":1},"data":[null]}`,
		`{"meta":{"total_count":1},"data":[{}]}`,
		`{"meta":{"total_count":2},"data":[` + memberFirstJSON + `,` + memberFirstJSON + `]}`,
		`{"meta":{"total_count":1},"data":[` + memberFirstJSON + `]} trailing`,
	}
	for _, field := range []string{"uuid", "email", "name", "role", "is_owner"} {
		value := `""`
		if field == "uuid" {
			value = `"` + memberFirstUUID + `"`
		}
		if field == "is_owner" {
			value = `false`
		}
		for _, replacement := range []string{"null", "42", "[]"} {
			member := strings.Replace(memberFirstJSON, `"`+field+`":`+value, `"`+field+`":`+replacement, 1)
			bodies = append(bodies, `{"meta":{"total_count":1},"data":[`+member+`]}`)
		}
		member := strings.Replace(memberFirstJSON, `"`+field+`":`+value, `"ignored":null`, 1)
		bodies = append(bodies, `{"meta":{"total_count":1},"data":[`+member+`]}`)
	}
	for _, id := range []string{"", "not-a-uuid", "../members", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "00000000-0000-0000-0000-000000000000"} {
		member := strings.Replace(memberFirstJSON, memberFirstUUID, id, 1)
		bodies = append(bodies, `{"meta":{"total_count":1},"data":[`+member+`]}`)
	}
	for _, createdAt := range []string{"42", "false", "{}"} {
		member := strings.TrimSuffix(memberFirstJSON, "}") + `,"created_at":` + createdAt + `}`
		bodies = append(bodies, `{"meta":{"total_count":1},"data":[`+member+`]}`)
	}
	for i, body := range bodies {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			members, err := c.ListMembers(context.Background())
			if err == nil || members != nil || (body != "" && strings.Contains(err.Error(), body)) {
				t.Fatal("invalid member response was accepted, partially returned, or echoed")
			}
		})
	}
}

func TestListMembersAPIErrorsAreRedactedAndNotRetried(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 429, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/management/v1/members" {
					t.Error("unexpected member operation")
				}
				w.WriteHeader(code)
				fmt.Fprint(w, `{"error":"management-secret private@example.test"}`)
			}))
			defer server.Close()
			c, _ := New(server.URL, "management-secret", "test")
			members, err := c.ListMembers(context.Background())
			var apiErr *APIError
			if members != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != code || strings.Contains(err.Error(), "management-secret") || strings.Contains(err.Error(), "private@example.test") || IsNotFound(err) != (code == 404) {
				t.Fatal("wrong status classification or private response contents exposed")
			}
			if requests.Load() != 1 {
				t.Fatal("member list request was retried")
			}
		})
	}
}

func TestListMembersPreservesRequestCancellation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	c, _ := New(server.URL, "management-secret", "test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ListMembers(ctx)
	if !errors.Is(err, context.Canceled) || requests.Load() != 0 {
		t.Fatal("canceled lookup reached API or lost context error")
	}
}
