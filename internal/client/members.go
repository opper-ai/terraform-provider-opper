package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
)

// Member describes a member of the management key's organization. CreatedAt
// refers to account creation, rather than when the member joined the organization.
type Member struct {
	UUID      string  `json:"uuid"`
	Email     string  `json:"email"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	IsOwner   bool    `json:"is_owner"`
	CreatedAt *string `json:"created_at,omitempty"`
}

var memberUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ListMembers reads the complete organization member list using members:read.
// Rejecting a partial list prevents a lookup from silently treating an omitted
// member as absent. UUID ordering makes results stable across backend row orders.
// This client does not expose member mutations or invitations.
func (c *Client) ListMembers(ctx context.Context) ([]Member, error) {
	data, err := c.do(ctx, http.MethodGet, "/management/v1/members", nil)
	if err != nil {
		return nil, err
	}
	// Required wire fields use pointers so missing/null values cannot silently
	// become valid empty strings or a false ownership flag.
	type wireMember struct {
		UUID      *string `json:"uuid"`
		Email     *string `json:"email"`
		Name      *string `json:"name"`
		Role      *string `json:"role"`
		IsOwner   *bool   `json:"is_owner"`
		CreatedAt *string `json:"created_at"`
	}
	var envelope struct {
		Data *[]*wireMember `json:"data"`
		Meta *struct {
			TotalCount *int `json:"total_count"`
		} `json:"meta"`
	}
	invalidResponse := errors.New("Opper Management API returned an invalid or incomplete member list response")
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil || envelope.Meta == nil || envelope.Meta.TotalCount == nil || *envelope.Meta.TotalCount != len(*envelope.Data) {
		return nil, invalidResponse
	}
	members := make([]Member, 0, len(*envelope.Data))
	seen := make(map[string]struct{}, len(*envelope.Data))
	for _, m := range *envelope.Data {
		if m == nil || m.UUID == nil || m.Email == nil || m.Name == nil || m.Role == nil || m.IsOwner == nil || !memberUUIDPattern.MatchString(*m.UUID) || *m.UUID == "00000000-0000-0000-0000-000000000000" {
			return nil, invalidResponse
		}
		if _, duplicate := seen[*m.UUID]; duplicate {
			return nil, invalidResponse
		}
		seen[*m.UUID] = struct{}{}
		members = append(members, Member{UUID: *m.UUID, Email: *m.Email, Name: *m.Name, Role: *m.Role, IsOwner: *m.IsOwner, CreatedAt: m.CreatedAt})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].UUID < members[j].UUID })
	return members, nil
}
