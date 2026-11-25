package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ProjectModelList is a project's own compat model list. It replaces the
// organization's list for the project's API keys. A project without one
// follows the organization and is absent from ListProjectModelLists.
type ProjectModelList struct {
	ProjectUUID     string `json:"project_uuid"`
	ProjectName     string `json:"project_name"`
	CompatModelList string `json:"compat_model_list"`
	DynamicRoutes   bool   `json:"compat_model_list_dynamic_routes"`
	// Enforced reports whether the project limits its own API keys to what
	// its list shows. While the organization enforces its list, every project
	// is limited whatever this says. A server that predates the field leaves
	// it out; that is read as false.
	Enforced bool `json:"compat_model_list_enforced"`
}

// ProjectModelListChange is one PUT body. The list and the routes switch are
// both required; a nil Enforced is left out and the project keeps what it had
// (off for a project that followed the organization).
type ProjectModelListChange struct {
	CompatModelList string `json:"compat_model_list"`
	DynamicRoutes   bool   `json:"compat_model_list_dynamic_routes"`
	Enforced        *bool  `json:"compat_model_list_enforced,omitempty"`
}

func projectModelListPath(projectUUID string) (string, error) {
	if !modelListOrganizationUUID.MatchString(projectUUID) {
		return "", errors.New("invalid project UUID; use the canonical lowercase UUID")
	}
	return "/management/v1/projects/" + projectUUID + "/model-list", nil
}

type projectModelListWire struct {
	ProjectUUID     string  `json:"project_uuid"`
	ProjectName     *string `json:"project_name"`
	CompatModelList string  `json:"compat_model_list"`
	DynamicRoutes   *bool   `json:"compat_model_list_dynamic_routes"`
	Enforced        *bool   `json:"compat_model_list_enforced"`
}

func (w *projectModelListWire) decode() (ProjectModelList, bool) {
	if w == nil || !modelListOrganizationUUID.MatchString(w.ProjectUUID) || w.ProjectName == nil || !validCompatModelList(w.CompatModelList) || w.DynamicRoutes == nil {
		return ProjectModelList{}, false
	}
	return ProjectModelList{
		ProjectUUID:     w.ProjectUUID,
		ProjectName:     *w.ProjectName,
		CompatModelList: w.CompatModelList,
		DynamicRoutes:   *w.DynamicRoutes,
		Enforced:        w.Enforced != nil && *w.Enforced,
	}, true
}

var errInvalidProjectModelList = errors.New("Opper Management API returned an invalid project model-list response")

// ListProjectModelLists returns every live project of the credential's
// organization that has its own list. The API returns them all in one page.
func (c *Client) ListProjectModelLists(ctx context.Context) ([]ProjectModelList, error) {
	data, err := c.do(ctx, http.MethodGet, "/management/v1/organization/model-list/projects", nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data *[]*projectModelListWire `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Data == nil {
		return nil, errInvalidProjectModelList
	}
	out := make([]ProjectModelList, 0, len(*envelope.Data))
	seen := make(map[string]bool, len(*envelope.Data))
	for _, item := range *envelope.Data {
		value, ok := item.decode()
		if !ok || seen[value.ProjectUUID] {
			return nil, errInvalidProjectModelList
		}
		seen[value.ProjectUUID] = true
		out = append(out, value)
	}
	return out, nil
}

// GetProjectModelList finds one project's own list. found is false when the
// project follows the organization, or does not exist in the organization:
// the API does not tell the two apart.
func (c *Client) GetProjectModelList(ctx context.Context, projectUUID string) (value ProjectModelList, found bool, err error) {
	if _, err = projectModelListPath(projectUUID); err != nil {
		return ProjectModelList{}, false, err
	}
	all, err := c.ListProjectModelLists(ctx)
	if err != nil {
		return ProjectModelList{}, false, err
	}
	for _, item := range all {
		if item.ProjectUUID == projectUUID {
			return item, true, nil
		}
	}
	return ProjectModelList{}, false, nil
}

// SetProjectModelList gives a project its own list with one PUT and never
// retries it. HTTP 404 means the project is missing, deleted or in another
// organization.
func (c *Client) SetProjectModelList(ctx context.Context, projectUUID string, change ProjectModelListChange) (ProjectModelList, error) {
	path, err := projectModelListPath(projectUUID)
	if err != nil {
		return ProjectModelList{}, err
	}
	if !validCompatModelList(change.CompatModelList) {
		return ProjectModelList{}, errors.New("compat_model_list must be all, simplified, recommended, or none")
	}
	if change.CompatModelList == "none" && !change.DynamicRoutes {
		return ProjectModelList{}, ErrNothingListed
	}
	data, err := c.do(ctx, http.MethodPut, path, change)
	if err != nil {
		return ProjectModelList{}, err
	}
	var envelope struct {
		Data *projectModelListWire `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ProjectModelList{}, errInvalidProjectModelList
	}
	value, ok := envelope.Data.decode()
	if !ok {
		return ProjectModelList{}, errInvalidProjectModelList
	}
	if value.ProjectUUID != projectUUID {
		return ProjectModelList{}, errors.New("Opper Management API returned a different project UUID")
	}
	return value, nil
}

// ClearProjectModelList removes the project's own list so it follows the
// organization again. Clearing a project that already follows it succeeds.
func (c *Client) ClearProjectModelList(ctx context.Context, projectUUID string) error {
	path, err := projectModelListPath(projectUUID)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodDelete, path, nil)
	return err
}
