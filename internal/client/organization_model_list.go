package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

// OrganizationModelList describes the credential's organization preference.
// It changes model listing, and with Enforced also which models the
// organization's API keys can call.
type OrganizationModelList struct {
	OrganizationUUID string `json:"organization_uuid"`
	CompatModelList  string `json:"compat_model_list"`
	// DynamicRoutes reports whether the organization's dynamic routes are
	// listed alongside the models chosen by CompatModelList.
	DynamicRoutes bool `json:"compat_model_list_dynamic_routes"`
	// Enforced reports whether the organization's API keys can only call what
	// the list shows. A server that predates the field leaves it out; that is
	// read as false, since such a server never limits calls to the list.
	Enforced bool `json:"compat_model_list_enforced"`
}

// OrganizationModelListChange is one PUT body. A nil field is left out of the
// body, and the API keeps its current value.
type OrganizationModelListChange struct {
	CompatModelList *string `json:"compat_model_list,omitempty"`
	DynamicRoutes   *bool   `json:"compat_model_list_dynamic_routes,omitempty"`
	Enforced        *bool   `json:"compat_model_list_enforced,omitempty"`
}

var modelListOrganizationUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// CompatModelListValues are the accepted compat_model_list values. "none"
// lists only the organization's dynamic routes.
var CompatModelListValues = []string{"all", "simplified", "recommended", "none"}

// ErrNothingListed is the combination the API refuses: none lists only dynamic
// routes, so turning the routes off would list nothing.
var ErrNothingListed = errors.New("compat_model_list none lists only dynamic routes, so compat_model_list_dynamic_routes must be true")

func validCompatModelList(value string) bool {
	for _, v := range CompatModelListValues {
		if value == v {
			return true
		}
	}
	return false
}

func (c *Client) GetOrganizationModelList(ctx context.Context) (OrganizationModelList, error) {
	return c.organizationModelList(ctx, http.MethodGet, nil)
}

// SetOrganizationModelList issues one desired-value PUT and never retries it.
// Callers must verify the credential organization before invoking this method.
func (c *Client) SetOrganizationModelList(ctx context.Context, change OrganizationModelListChange) (OrganizationModelList, error) {
	if change.CompatModelList == nil && change.DynamicRoutes == nil && change.Enforced == nil {
		return OrganizationModelList{}, errors.New("set one or more of compat_model_list, compat_model_list_dynamic_routes and compat_model_list_enforced")
	}
	if change.CompatModelList != nil && !validCompatModelList(*change.CompatModelList) {
		return OrganizationModelList{}, errors.New("compat_model_list must be all, simplified, recommended, or none")
	}
	if change.CompatModelList != nil && *change.CompatModelList == "none" && change.DynamicRoutes != nil && !*change.DynamicRoutes {
		return OrganizationModelList{}, ErrNothingListed
	}
	return c.organizationModelList(ctx, http.MethodPut, change)
}

func (c *Client) organizationModelList(ctx context.Context, method string, body any) (OrganizationModelList, error) {
	data, err := c.do(ctx, method, "/management/v1/organization/model-list", body)
	if err != nil {
		return OrganizationModelList{}, err
	}
	var envelope struct {
		Data *struct {
			OrganizationUUID string `json:"organization_uuid"`
			CompatModelList  string `json:"compat_model_list"`
			DynamicRoutes    *bool  `json:"compat_model_list_dynamic_routes"`
			Enforced         *bool  `json:"compat_model_list_enforced"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil || !modelListOrganizationUUID.MatchString(envelope.Data.OrganizationUUID) || !validCompatModelList(envelope.Data.CompatModelList) || envelope.Data.DynamicRoutes == nil {
		return OrganizationModelList{}, errors.New("Opper Management API returned an invalid organization model-list response")
	}
	return OrganizationModelList{
		OrganizationUUID: envelope.Data.OrganizationUUID,
		CompatModelList:  envelope.Data.CompatModelList,
		DynamicRoutes:    *envelope.Data.DynamicRoutes,
		Enforced:         envelope.Data.Enforced != nil && *envelope.Data.Enforced,
	}, nil
}
