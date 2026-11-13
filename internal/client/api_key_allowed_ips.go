package client

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
)

var errAPIKeyAllowedIPsUnconfirmed = errors.New("Opper Management API did not confirm the requested API key IP allowlist; inspect the saved key before attempting recovery")

// CreateAPIKeyWithAllowedIPs sends one explicit project-key creation. Restricted
// creation first checks the advertised POST and PATCH contracts. That discovery
// is not a guarantee about a different replica during a mixed rollout, so the
// response must confirm the policy. A known ID and one-time secret are retained
// alongside an error when confirmation fails; no PATCH, delete, or retry follows.
func (c *Client) CreateAPIKeyWithAllowedIPs(ctx context.Context, projectID, name, idempotencyKey string, allowedIPs []string) (APIKey, error) {
	path, err := apiKeysPath(projectID)
	if err != nil {
		return APIKey{}, err
	}
	ips, err := NormalizeAllowedIPs(allowedIPs)
	if err != nil {
		return APIKey{}, err
	}
	if len(ips) > 0 {
		if err := c.requireAPIKeyAllowedIPs(ctx); err != nil {
			return APIKey{}, err
		}
	}
	body := struct {
		Name           string   `json:"name"`
		Kind           string   `json:"kind"`
		ReturnSecret   bool     `json:"return_secret"`
		IdempotencyKey string   `json:"idempotency_key,omitempty"`
		AllowedIPs     []string `json:"allowed_ips"`
	}{Name: name, Kind: "project", ReturnSecret: true, IdempotencyKey: idempotencyKey, AllowedIPs: ips}
	raw, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return APIKey{}, err
	}
	key, err := decodeAPIKeyEnvelope(raw, true)
	if err != nil {
		return key, err
	}
	if key.ID <= 0 || key.Kind != "project" || key.Name != name || key.AllowedIPs == nil || !slices.Equal(key.AllowedIPs, ips) {
		return key, errAPIKeyAllowedIPsUnconfirmed
	}
	return key, nil
}

// UpdateAPIKeyAllowedIPs replaces only the list on a known project-owned key.
// Nil input is sent as [], explicitly clearing restrictions. A failed or
// unconfirmed response is never retried. Any unexpected secret is discarded.
func (c *Client) UpdateAPIKeyAllowedIPs(ctx context.Context, projectID string, id int64, allowedIPs []string) (APIKey, error) {
	path, err := apiKeysPath(projectID)
	if err != nil {
		return APIKey{}, err
	}
	if id <= 0 || id > math.MaxInt32 {
		return APIKey{}, &localValidationError{message: "invalid API key ID"}
	}
	ips, err := NormalizeAllowedIPs(allowedIPs)
	if err != nil {
		return APIKey{}, err
	}
	raw, err := c.do(ctx, http.MethodPatch, path+"/"+strconv.FormatInt(id, 10), struct {
		AllowedIPs []string `json:"allowed_ips"`
	}{ips})
	if err != nil {
		return APIKey{}, err
	}
	key, err := decodeAPIKeyEnvelope(raw, false)
	if err != nil {
		return key, err
	}
	if key.ID != id || key.Kind != "project" || key.AllowedIPs == nil || !slices.Equal(key.AllowedIPs, ips) {
		return key, errAPIKeyAllowedIPsUnconfirmed
	}
	return key, nil
}
