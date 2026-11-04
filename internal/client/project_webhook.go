package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ProjectWebhook contains only the project's default completion URL and secret
// timestamps. The overview endpoint does not retrieve or initialize a signing
// secret. A URL can contain credentials, so callers must not log this value.
type ProjectWebhook struct {
	URL                      *string `json:"url"`
	URLUpdatedAt             *string `json:"url_updated_at"`
	SecretCreatedAt          *string `json:"secret_created_at"`
	PreviousSecretValidUntil *string `json:"previous_secret_valid_until"`
}

func (c *Client) GetProjectWebhook(ctx context.Context, projectID string) (ProjectWebhook, error) {
	path, err := projectPath(projectID)
	if err != nil {
		return ProjectWebhook{}, err
	}
	return c.projectWebhook(ctx, http.MethodGet, path+"/webhook", nil)
}

// UpdateProjectWebhook replaces the default URL. A nil URL explicitly clears
// it; the url field is never omitted from PATCH, where omission is invalid.
func (c *Client) UpdateProjectWebhook(ctx context.Context, projectID string, url *string) (ProjectWebhook, error) {
	path, err := projectPath(projectID)
	if err != nil {
		return ProjectWebhook{}, err
	}
	body := struct {
		URL *string `json:"url"`
	}{URL: url}
	return c.projectWebhook(ctx, http.MethodPatch, path+"/webhook", body)
}

func (c *Client) projectWebhook(ctx context.Context, method, path string, body any) (ProjectWebhook, error) {
	data, err := c.do(ctx, method, path, body)
	if err != nil {
		return ProjectWebhook{}, err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ProjectWebhook{}, errors.New("Opper Management API returned an invalid project webhook response")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(envelope.Data, &fields) != nil || fields == nil || fields["url"] == nil {
		// An omitted URL is malformed, not proof that a previously configured
		// webhook was cleared. Preserve that distinction before decoding pointers.
		return ProjectWebhook{}, errors.New("Opper Management API returned an invalid project webhook response")
	}
	var overview ProjectWebhook
	if json.Unmarshal(envelope.Data, &overview) != nil {
		return ProjectWebhook{}, errors.New("Opper Management API returned an invalid project webhook response")
	}
	return overview, nil
}
