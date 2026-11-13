package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// InitialProjectAccess is the creation-only access policy committed atomically
// with a project. Ongoing policy changes use the separate project-access API.
type InitialProjectAccess struct {
	Mode      string   `json:"mode"`
	RoleKeys  []string `json:"role_keys"`
	UserUUIDs []string `json:"user_uuids"`
}

// CreateProjectWithAccess sends one project creation request with its initial
// policy. Denials, unsupported endpoints and uncertain outcomes are returned
// without retries or a fallback to creating an unrestricted project. The
// dedicated endpoint fails closed on older servers whose legacy create route
// would silently ignore an access policy.
func (c *Client) CreateProjectWithAccess(ctx context.Context, name, description string, access InitialProjectAccess) (Project, error) {
	if !projectAccessRecipientsValid(access.Mode, access.RoleKeys, access.UserUUIDs) {
		return Project{}, &localValidationError{message: "invalid initial project access policy"}
	}
	access.RoleKeys = canonicalProjectAccessRecipients(access.RoleKeys)
	access.UserUUIDs = canonicalProjectAccessRecipients(access.UserUUIDs)
	body := struct {
		Name        string               `json:"name"`
		Description string               `json:"description"`
		Access      InitialProjectAccess `json:"access"`
	}{Name: name, Description: description, Access: access}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > maxProjectAccessInputBytes {
		return Project{}, &localValidationError{message: "project creation with initial access exceeds the request size limit"}
	}
	return c.project(ctx, http.MethodPost, "/management/v1/projects/with-access", body)
}
