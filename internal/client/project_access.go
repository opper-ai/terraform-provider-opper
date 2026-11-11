package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
)

// ProjectAccessPolicy is the complete organization-bound snapshot returned by
// both project-access operations. Its token describes an observation, not a
// credential or a promise that a particular writer produced the snapshot.
type ProjectAccessPolicy struct {
	OrganizationUUID string   `json:"organization_uuid"`
	ProjectUUID      string   `json:"project_uuid"`
	Mode             string   `json:"mode"`
	RoleKeys         []string `json:"role_keys"`
	UserUUIDs        []string `json:"user_uuids"`
	Revision         int64    `json:"revision"`
	ChangeToken      string   `json:"change_token"`
}

// ProjectAccessInput replaces the whole policy using the token pinned by the
// reviewed plan. Empty grant slices are encoded as explicit arrays.
type ProjectAccessInput struct {
	Mode                string   `json:"mode"`
	RoleKeys            []string `json:"role_keys"`
	UserUUIDs           []string `json:"user_uuids"`
	ExpectedChangeToken string   `json:"expected_change_token"`
}

var projectAccessTokenRE = regexp.MustCompile(`^pav1_[A-Za-z0-9_-]{43}$`)
var errProjectAccessResponse = errors.New("Opper Management API returned an invalid or incomplete project access policy response")

const maxProjectAccessInputBytes = 32768

func projectAccessPath(projectUUID string) (string, error) {
	if !validCustomModelID(projectUUID) {
		return "", &localValidationError{message: "invalid project access project UUID"}
	}
	return "/management/v1/projects/" + projectUUID + "/access", nil
}

func projectAccessRecipientsValid(mode string, roles, users []string) bool {
	if mode != "organization" && mode != "restricted" || len(roles) > 500 || len(users) > 500 {
		return false
	}
	if mode == "organization" && (len(roles) != 0 || len(users) != 0) {
		return false
	}
	for _, role := range roles {
		if !assignableRole(role) {
			return false
		}
	}
	for _, user := range users {
		if !validCustomModelID(user) {
			return false
		}
	}
	return true
}

func canonicalProjectAccessRecipients(values []string) []string {
	result := append([]string{}, values...)
	slices.Sort(result)
	return slices.Compact(result)
}

func projectAccessInput(input ProjectAccessInput) (ProjectAccessInput, error) {
	if !projectAccessRecipientsValid(input.Mode, input.RoleKeys, input.UserUUIDs) || !projectAccessTokenRE.MatchString(input.ExpectedChangeToken) {
		return ProjectAccessInput{}, &localValidationError{message: "invalid project access replacement intent"}
	}
	input.RoleKeys = canonicalProjectAccessRecipients(input.RoleKeys)
	input.UserUUIDs = canonicalProjectAccessRecipients(input.UserUUIDs)
	encoded, err := json.Marshal(input)
	if err != nil || len(encoded) > maxProjectAccessInputBytes {
		return ProjectAccessInput{}, &localValidationError{message: "project access replacement exceeds the request size limit"}
	}
	return input, nil
}

func decodeProjectAccess(raw []byte, projectUUID string) (ProjectAccessPolicy, error) {
	var result ProjectAccessPolicy
	envelope, err := customModelObject(raw, "meta", "data")
	if err != nil {
		return result, errProjectAccessResponse
	}
	fields, err := customModelObject(envelope["data"], "organization_uuid", "project_uuid", "mode", "role_keys", "user_uuids", "revision", "change_token")
	if err != nil || json.Unmarshal(envelope["data"], &result) != nil {
		return ProjectAccessPolicy{}, errProjectAccessResponse
	}
	var revision *int64
	if json.Unmarshal(fields["revision"], &revision) != nil || revision == nil || *revision < 0 {
		return ProjectAccessPolicy{}, errProjectAccessResponse
	}
	if !validCustomModelID(result.OrganizationUUID) || result.ProjectUUID != projectUUID || !projectAccessTokenRE.MatchString(result.ChangeToken) || result.RoleKeys == nil || result.UserUUIDs == nil || !projectAccessRecipientsValid(result.Mode, result.RoleKeys, result.UserUUIDs) {
		return ProjectAccessPolicy{}, errProjectAccessResponse
	}
	if !slices.Equal(result.RoleKeys, canonicalProjectAccessRecipients(result.RoleKeys)) || !slices.Equal(result.UserUUIDs, canonicalProjectAccessRecipients(result.UserUUIDs)) {
		return ProjectAccessPolicy{}, errProjectAccessResponse
	}
	return result, nil
}

// GetProjectAccess performs one pure snapshot read. Failed or invalid reads
// must not be interpreted as an empty or Organization policy.
func (c *Client) GetProjectAccess(ctx context.Context, projectUUID string) (ProjectAccessPolicy, error) {
	path, err := projectAccessPath(projectUUID)
	if err != nil {
		return ProjectAccessPolicy{}, err
	}
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ProjectAccessPolicy{}, err
	}
	return decodeProjectAccess(raw, projectUUID)
}

// ReplaceProjectAccess sends the reviewed whole-policy intent once. Conflicts,
// uncertain responses and transport failures never fetch a new token or retry
// the mutation. The caller verifies its configured organization before writing.
func (c *Client) ReplaceProjectAccess(ctx context.Context, projectUUID string, input ProjectAccessInput) (ProjectAccessPolicy, error) {
	path, err := projectAccessPath(projectUUID)
	if err != nil {
		return ProjectAccessPolicy{}, err
	}
	input, err = projectAccessInput(input)
	if err != nil {
		return ProjectAccessPolicy{}, err
	}
	raw, err := c.do(ctx, http.MethodPut, path, input)
	if err != nil {
		return ProjectAccessPolicy{}, err
	}
	return decodeProjectAccess(raw, projectUUID)
}
