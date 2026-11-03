package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// APIKey is a runtime key. Key is available only when the key is first minted;
// listing or replaying a create cannot recover that secret. Do not log this
// struct or include it in diagnostics.
type APIKey struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	Prefix              string `json:"prefix"`
	Kind                string `json:"kind"`
	CreatedAt           string `json:"created_at,omitempty"`
	Key                 string `json:"key,omitempty"`
	IdempotencyReplayed bool   `json:"-"`
}

// CreateAPIKey mints a project-owned key, which survives removal of the member
// who created it. The API defaults to personal ownership if kind is omitted.
// A missing secret is intentionally not an error: callers must retain the
// returned ID before explaining that a replayed secret cannot be recovered.
func (c *Client) CreateAPIKey(ctx context.Context, projectID, name, idempotencyKey string) (APIKey, error) {
	path, err := apiKeysPath(projectID)
	if err != nil {
		return APIKey{}, err
	}
	body := struct {
		Name           string `json:"name"`
		Kind           string `json:"kind"`
		ReturnSecret   bool   `json:"return_secret"`
		IdempotencyKey string `json:"idempotency_key,omitempty"`
	}{Name: name, Kind: "project", ReturnSecret: true, IdempotencyKey: idempotencyKey}
	data, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return APIKey{}, err
	}
	var envelope struct {
		Data *APIKey `json:"data"`
		Meta struct {
			IdempotencyReplayed bool `json:"idempotency_replayed"`
		} `json:"meta"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil || envelope.Data.ID <= 0 || envelope.Data.Kind != "project" {
		return APIKey{}, errors.New("Opper Management API returned an invalid project API key response")
	}
	envelope.Data.IdempotencyReplayed = envelope.Meta.IdempotencyReplayed
	return *envelope.Data, nil
}

// ListAPIKeys returns metadata for the calling organization's selected project.
// The list includes both personal and project-owned runtime keys. No secret is
// retrievable here, and unexpected secret fields are discarded.
func (c *Client) ListAPIKeys(ctx context.Context, projectID string) ([]APIKey, error) {
	path, err := apiKeysPath(projectID)
	if err != nil {
		return nil, err
	}
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data *[]APIKey `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil {
		return nil, errors.New("Opper Management API returned an invalid API key list response")
	}
	keys := *envelope.Data
	for i := range keys {
		if keys[i].ID <= 0 {
			return nil, errors.New("Opper Management API returned an invalid API key list response")
		}
		keys[i].Key = ""
	}
	return keys, nil
}

func (c *Client) DeleteAPIKey(ctx context.Context, projectID string, id int64) error {
	path, err := apiKeysPath(projectID)
	if err != nil {
		return err
	}
	if id <= 0 {
		return errors.New("invalid API key ID")
	}
	_, err = c.do(ctx, http.MethodDelete, path+"/"+strconv.FormatInt(id, 10), nil)
	return err
}

func apiKeysPath(projectID string) (string, error) {
	path, err := projectPath(projectID)
	if err != nil {
		return "", err
	}
	return path + "/api-keys", nil
}
