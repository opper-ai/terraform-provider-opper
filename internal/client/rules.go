package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

// ErrRuleNotFound means a successful full rule list did not contain this ID.
// A 404 on the list endpoint is an API failure, not proof that a rule is gone.
var ErrRuleNotFound = errors.New("Opper rule was not found in the organization")

var ruleUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type RuleScope struct {
	Type          string   `json:"type"`
	ProjectUUIDs  []string `json:"project_uuids,omitempty"`
	FunctionUUIDs []string `json:"function_uuids,omitempty"`
	RoleNames     []string `json:"role_names,omitempty"`
	MemberUUIDs   []string `json:"member_uuids,omitempty"`
}

type Rule struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Name          *string         `json:"name,omitempty"`
	Enabled       bool            `json:"enabled"`
	Scope         RuleScope       `json:"scope"`
	SchemaVersion int64           `json:"schema_version"`
	Config        json.RawMessage `json:"config"`
	CreatedAt     string          `json:"created_at,omitempty"`
	UpdatedAt     string          `json:"updated_at,omitempty"`
}

// RulePatch contains only mutable fields. Nil omits a field, including false
// enabled values and empty names, which callers can send using pointers.
type RulePatch struct {
	Name          *string         `json:"name,omitempty"`
	Enabled       *bool           `json:"enabled,omitempty"`
	SchemaVersion *int64          `json:"schema_version,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
}

func (c *Client) CreateRule(ctx context.Context, rule Rule) (Rule, error) {
	if !validRule(rule) {
		return Rule{}, errors.New("invalid rule create request")
	}
	body := struct {
		ID             string          `json:"id"`
		Kind           string          `json:"kind"`
		Name           *string         `json:"name,omitempty"`
		Enabled        bool            `json:"enabled"`
		Scope          RuleScope       `json:"scope"`
		SchemaVersion  int64           `json:"schema_version"`
		Config         json.RawMessage `json:"config"`
		IdempotencyKey string          `json:"idempotency_key"`
	}{rule.ID, rule.Kind, rule.Name, rule.Enabled, rule.Scope, rule.SchemaVersion, rule.Config, "terraform:" + rule.ID}
	return c.rule(ctx, http.MethodPost, "/management/v1/controls/rules", body, rule.ID)
}

func (c *Client) ListRules(ctx context.Context) ([]Rule, error) {
	data, err := c.do(ctx, http.MethodGet, "/management/v1/controls/rules", nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data *[]Rule `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil {
		return nil, errors.New("Opper Management API returned an invalid rule list")
	}
	seen := make(map[string]bool)
	for _, rule := range *envelope.Data {
		if !validRule(rule) || seen[rule.ID] {
			return nil, errors.New("Opper Management API returned an invalid rule list")
		}
		seen[rule.ID] = true
	}
	return *envelope.Data, nil
}

func (c *Client) GetRule(ctx context.Context, id string) (Rule, error) {
	if !ruleUUIDPattern.MatchString(id) {
		return Rule{}, errors.New("invalid rule UUID")
	}
	rules, err := c.ListRules(ctx)
	if err != nil {
		return Rule{}, err
	}
	for _, rule := range rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return Rule{}, ErrRuleNotFound
}

func (c *Client) UpdateRule(ctx context.Context, id string, patch RulePatch) (Rule, error) {
	if !ruleUUIDPattern.MatchString(id) {
		return Rule{}, errors.New("invalid rule UUID")
	}
	if patch.Name == nil && patch.Enabled == nil && patch.SchemaVersion == nil && len(patch.Config) == 0 {
		return Rule{}, errors.New("rule update requires at least one changed field")
	}
	if len(patch.Config) > 0 && !ruleConfigObject(patch.Config) {
		return Rule{}, errors.New("rule config must be a JSON object")
	}
	return c.rule(ctx, http.MethodPatch, "/management/v1/controls/rules/"+id, patch, id)
}

func (c *Client) DeleteRule(ctx context.Context, id string) error {
	if !ruleUUIDPattern.MatchString(id) {
		return errors.New("invalid rule UUID")
	}
	_, err := c.do(ctx, http.MethodDelete, "/management/v1/controls/rules/"+id, nil)
	return err
}

func (c *Client) rule(ctx context.Context, method, path string, body any, id string) (Rule, error) {
	data, err := c.do(ctx, method, path, body)
	if err != nil {
		return Rule{}, err
	}
	var envelope struct {
		Data *Rule `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || envelope.Data == nil || !validRule(*envelope.Data) || envelope.Data.ID != id {
		return Rule{}, errors.New("Opper Management API returned an invalid rule response")
	}
	return *envelope.Data, nil
}

func validRule(rule Rule) bool {
	if !ruleUUIDPattern.MatchString(rule.ID) || rule.SchemaVersion < 1 || !ruleConfigObject(rule.Config) {
		return false
	}
	switch rule.Kind {
	case "guard", "observe", "route", "comply":
	default:
		return false
	}
	scope := rule.Scope
	targets := map[string][]string{"projects": scope.ProjectUUIDs, "functions": scope.FunctionUUIDs, "roles": scope.RoleNames, "members": scope.MemberUUIDs}
	switch scope.Type {
	case "org", "projects", "functions", "roles", "members":
	default:
		return false
	}
	for targetType, values := range targets {
		if (targetType == scope.Type && len(values) == 0) || (targetType != scope.Type && len(values) > 0) {
			return false
		}
		for _, value := range values {
			if value == "" || (targetType != "roles" && !ruleUUIDPattern.MatchString(value)) {
				return false
			}
		}
	}
	return true
}

func ruleConfigObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}
