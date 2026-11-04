package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

// OrganizationModelList describes the credential's organization preference,
// which changes model listing only, not model execution permissions.
type OrganizationModelList struct {
	OrganizationUUID string `json:"organization_uuid"`
	CompatModelList  string `json:"compat_model_list"`
}

var modelListOrganizationUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validCompatModelList(value string) bool {
	return value == "all" || value == "simplified" || value == "recommended"
}

func (c *Client) GetOrganizationModelList(ctx context.Context) (OrganizationModelList, error) {
	return c.organizationModelList(ctx, http.MethodGet, nil)
}

// SetOrganizationModelList issues one desired-value PUT and never retries it.
// Callers must verify the credential organization before invoking this method.
func (c *Client) SetOrganizationModelList(ctx context.Context, value string) (OrganizationModelList, error) {
	if !validCompatModelList(value) {
		return OrganizationModelList{}, errors.New("compat_model_list must be all, simplified, or recommended")
	}
	return c.organizationModelList(ctx, http.MethodPut, map[string]string{"compat_model_list": value})
}

func (c *Client) organizationModelList(ctx context.Context, method string, body any) (OrganizationModelList, error) {
	data, err := c.do(ctx, method, "/management/v1/organization/model-list", body)
	if err != nil {
		return OrganizationModelList{}, err
	}
	var envelope struct {
		Data *OrganizationModelList `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil || !modelListOrganizationUUID.MatchString(envelope.Data.OrganizationUUID) || !validCompatModelList(envelope.Data.CompatModelList) {
		return OrganizationModelList{}, errors.New("Opper Management API returned an invalid organization model-list response")
	}
	return *envelope.Data, nil
}
