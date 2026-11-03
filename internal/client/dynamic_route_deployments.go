package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

// DynamicRouteVersion is an immutable version, not the current editable draft.
type DynamicRouteVersion struct {
	UUID          string          `json:"uuid"`
	VersionNumber int64           `json:"version_number"`
	RouteName     string          `json:"route_name"`
	Description   string          `json:"description"`
	Graph         json.RawMessage `json:"graph"`
	Note          string          `json:"note"`
	DraftRevision int64           `json:"draft_revision"`
	CreatedAt     string          `json:"created_at"`
}

// DynamicRouteDeployOptions always supplies all three compare-and-swap
// preconditions. Zero ActiveVersionNumber means the route must be inactive.
type DynamicRouteDeployOptions struct {
	RouteUUID           string `json:"expected_route_uuid"`
	DraftRevision       int64  `json:"expected_draft_revision"`
	ActiveVersionNumber int64  `json:"expected_active_version_number"`
	IdempotencyKey      string `json:"idempotency_key"`
	Note                string `json:"note,omitempty"`
}

type DynamicRouteDeploymentResult struct {
	Detail                  DynamicRouteDetail
	DeployedVersion         *DynamicRouteVersion
	UndeployedVersion       *DynamicRouteVersion
	UndeployedVersionNumber *int64
	IdempotencyReplayed     bool
}

var dynamicRouteOperationRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func validDynamicRouteVersion(v *DynamicRouteVersion, name string) bool {
	return v != nil && v.UUID != "" && v.VersionNumber > 0 && v.DraftRevision > 0 && v.RouteName == name && len(v.Graph) > 0 && string(v.Graph) != "null"
}

func (c *Client) GetDynamicRouteVersion(ctx context.Context, name string, versionNumber int64) (DynamicRouteVersion, error) {
	path, err := dynamicRoutePath(name)
	if err != nil || versionNumber < 1 {
		return DynamicRouteVersion{}, errors.New("invalid dynamic route version identity")
	}
	data, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/versions/%d", path, versionNumber), nil)
	if err != nil {
		return DynamicRouteVersion{}, err
	}
	var envelope struct {
		Data *DynamicRouteVersion `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || !validDynamicRouteVersion(envelope.Data, name) || envelope.Data.VersionNumber != versionNumber {
		return DynamicRouteVersion{}, errors.New("Opper returned an invalid immutable dynamic route version")
	}
	return *envelope.Data, nil
}

func (c *Client) DeployDynamicRoute(ctx context.Context, name string, options DynamicRouteDeployOptions) (DynamicRouteDeploymentResult, error) {
	path, err := dynamicRoutePath(name)
	if err != nil {
		return DynamicRouteDeploymentResult{}, err
	}
	if options.RouteUUID == "" || options.DraftRevision < 1 || options.ActiveVersionNumber < 0 || !dynamicRouteOperationRE.MatchString(options.IdempotencyKey) {
		return DynamicRouteDeploymentResult{}, errors.New("conditional deployment requires a route UUID, positive draft revision, nonnegative active version and idempotency key")
	}
	return c.dynamicRouteDeployment(ctx, http.MethodPost, path+"/deployments", name, options)
}

func (c *Client) UndeployDynamicRoute(ctx context.Context, name, routeUUID string, activeVersionNumber int64, idempotencyKey string) (DynamicRouteDeploymentResult, error) {
	path, err := dynamicRoutePath(name)
	if err != nil {
		return DynamicRouteDeploymentResult{}, err
	}
	if routeUUID == "" || activeVersionNumber < 0 || !dynamicRouteOperationRE.MatchString(idempotencyKey) {
		return DynamicRouteDeploymentResult{}, errors.New("conditional undeployment requires a route UUID, nonnegative owned version and idempotency key")
	}
	body := struct {
		RouteUUID           string `json:"expected_route_uuid"`
		ActiveVersionNumber int64  `json:"expected_active_version_number"`
		IdempotencyKey      string `json:"idempotency_key"`
	}{routeUUID, activeVersionNumber, idempotencyKey}
	return c.dynamicRouteDeployment(ctx, http.MethodPost, path+"/undeploy", name, body)
}

// GetDynamicRouteDeploymentReceipt is a pure read. It never retries a POST or
// reactivates the recorded historical version when another writer has changed
// the active pointer since that operation completed.
func (c *Client) GetDynamicRouteDeploymentReceipt(ctx context.Context, name, idempotencyKey string) (DynamicRouteDeploymentResult, error) {
	path, err := dynamicRoutePath(name)
	if err != nil {
		return DynamicRouteDeploymentResult{}, err
	}
	if !dynamicRouteOperationRE.MatchString(idempotencyKey) {
		return DynamicRouteDeploymentResult{}, errors.New("invalid dynamic route operation ID")
	}
	return c.dynamicRouteDeployment(ctx, http.MethodGet, path+"/deployments/"+idempotencyKey, name, nil)
}

func (c *Client) dynamicRouteDeployment(ctx context.Context, method, path, name string, body any) (DynamicRouteDeploymentResult, error) {
	data, err := c.do(ctx, method, path, body)
	if err != nil {
		return DynamicRouteDeploymentResult{}, err
	}
	var envelope struct {
		Data *DynamicRouteDetail `json:"data"`
		Meta struct {
			DeployedVersion         *DynamicRouteVersion `json:"deployed_version"`
			UndeployedVersion       *DynamicRouteVersion `json:"undeployed_version"`
			UndeployedVersionNumber *int64               `json:"undeployed_version_number"`
			IdempotencyReplayed     bool                 `json:"idempotency_replayed"`
		} `json:"meta"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Data == nil || envelope.Data.Route.Name != name || envelope.Data.Route.UUID == "" || envelope.Data.Draft.Revision < 1 {
		return DynamicRouteDeploymentResult{}, errors.New("Opper returned an invalid dynamic route operation receipt")
	}
	m := envelope.Meta
	if m.DeployedVersion != nil {
		if !validDynamicRouteVersion(m.DeployedVersion, name) || m.UndeployedVersionNumber != nil {
			return DynamicRouteDeploymentResult{}, errors.New("Opper returned an invalid deployed version receipt")
		}
	} else if m.UndeployedVersionNumber == nil || *m.UndeployedVersionNumber < 0 || (*m.UndeployedVersionNumber > 0 && (!validDynamicRouteVersion(m.UndeployedVersion, name) || m.UndeployedVersion.VersionNumber != *m.UndeployedVersionNumber)) {
		return DynamicRouteDeploymentResult{}, errors.New("Opper returned an incomplete undeployment receipt")
	}
	return DynamicRouteDeploymentResult{Detail: *envelope.Data, DeployedVersion: m.DeployedVersion, UndeployedVersion: m.UndeployedVersion, UndeployedVersionNumber: m.UndeployedVersionNumber, IdempotencyReplayed: m.IdempotencyReplayed}, nil
}
