package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDynamicRouteDeploymentContract(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authorization")
		}
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["expected_route_uuid"] != "route-uuid" || body["expected_active_version_number"] != float64(0) || body["idempotency_key"] != "operation-id" {
				t.Errorf("missing atomic preconditions: %#v", body)
			}
			if r.URL.Path == "/management/v1/dynamic-routes/app/deployments" && body["expected_draft_revision"] != float64(3) {
				t.Error("missing draft revision")
			}
		}
		if r.URL.Path == "/management/v1/dynamic-routes/app/versions/1" {
			fmt.Fprint(w, `{"data":{"uuid":"version-uuid","version_number":1,"route_name":"app","draft_revision":3,"graph":{"elements":[]}}}`)
			return
		}
		meta := `"deployed_version":{"uuid":"version-uuid","version_number":1,"route_name":"app","draft_revision":3,"graph":{"elements":[]}},"idempotency_replayed":true`
		if r.URL.Path == "/management/v1/dynamic-routes/app/undeploy" {
			meta = `"undeployed_version_number":0,"undeployed_version":null,"idempotency_replayed":false`
		}
		fmt.Fprintf(w, `{"data":{"route":{"uuid":"route-uuid","name":"app"},"draft":{"graph":{"elements":[]},"revision":3}},"meta":{%s}}`, meta)
	}))
	defer server.Close()
	c, _ := New(server.URL, "secret", "test")
	result, err := c.DeployDynamicRoute(context.Background(), "app", DynamicRouteDeployOptions{RouteUUID: "route-uuid", DraftRevision: 3, ActiveVersionNumber: 0, IdempotencyKey: "operation-id"})
	if err != nil || result.DeployedVersion == nil || result.DeployedVersion.UUID != "version-uuid" || !result.IdempotencyReplayed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := c.GetDynamicRouteDeploymentReceipt(context.Background(), "app", "operation-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetDynamicRouteVersion(context.Background(), "app", 1); err != nil {
		t.Fatal(err)
	}
	result, err = c.UndeployDynamicRoute(context.Background(), "app", "route-uuid", 0, "operation-id")
	if err != nil || result.UndeployedVersionNumber == nil || *result.UndeployedVersionNumber != 0 || result.DeployedVersion != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(calls) != 4 || calls[1] != "GET /management/v1/dynamic-routes/app/deployments/operation-id" {
		t.Fatalf("unexpected calls: %v", calls)
	}
}
