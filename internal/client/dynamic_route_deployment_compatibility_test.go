package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Older backends accept unknown fields on /deploy. A fallback would therefore
// turn a guarded deployment into an unconditional write during a rolling
// upgrade, before the response could tell the client which backend handled it.
func TestDeploymentNeverFallsBackToLegacyEndpoint(t *testing.T) {
	var legacyWrites, guardedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/v1/dynamic-routes/support/deploy":
			legacyWrites.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/management/v1/dynamic-routes/support/deployments":
			guardedRequests.Add(1)
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected fallback request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c, err := New(server.URL, "op-mak-synthetic-compatibility-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.DeployDynamicRoute(context.Background(), "support", DynamicRouteDeployOptions{
		RouteUUID: "571bd22f-37b6-4cd4-a09e-615cc4332970", DraftRevision: 1,
		ActiveVersionNumber: 0, IdempotencyKey: "new-backend-required",
	})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusNotFound {
		t.Fatalf("unsupported backend should fail with 404, got %v", err)
	}
	if guardedRequests.Load() != 1 || legacyWrites.Load() != 0 {
		t.Fatalf("guarded=%d legacy writes=%d", guardedRequests.Load(), legacyWrites.Load())
	}
}
