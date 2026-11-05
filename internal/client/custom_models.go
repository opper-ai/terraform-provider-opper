package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// CustomModel is descriptive metadata, not a round-trippable connection
// definition. No credential or arbitrary legacy extras are exposed.
type CustomModel struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Identifier        string  `json:"identifier"`
	Provider          *string `json:"provider"`
	Type              *string `json:"type"`
	CredentialPresent bool    `json:"credential_present"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

type CustomModelState struct {
	Model       CustomModel
	ChangeToken string `json:"-"`
}

func (CustomModelState) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "custom model state (validator redacted)")
}

type CustomModelCreationReceipt struct {
	IdempotencyKey  string `json:"idempotency_key"`
	CustomModelUUID string `json:"custom_model_uuid"`
	ResourceExists  bool   `json:"resource_exists"`
}

type CustomModelCreationResult struct {
	Model     CustomModel
	Operation CustomModelCreationReceipt
	Replayed  bool
}

// A nil credential preserves the exact stored credential. A non-nil credential
// is an explicit rotation intent. Never serialize or log this private input.
type CustomModelUpdateInput struct {
	ExpectedChangeToken string  `json:"-"`
	IdempotencyKey      string  `json:"idempotency_key"`
	Name                *string `json:"name,omitempty"`
	Identifier          *string `json:"identifier,omitempty"`
	Credential          *string `json:"-"`
}

func (CustomModelUpdateInput) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "custom model update input (redacted)")
}

// Update receipts describe a historical commit, not permission to reapply it.
// A nil result match means the original signing key is unavailable or changed.
type CustomModelUpdateReceipt struct {
	IdempotencyKey            string `json:"idempotency_key"`
	CustomModelUUID           string `json:"custom_model_uuid"`
	ResourceExists            bool   `json:"resource_exists"`
	ResultMatchesCurrentState *bool  `json:"result_matches_current_state"`
}

type CustomModelUpdateResult struct {
	Operation CustomModelUpdateReceipt
	Replayed  bool
}

var customModelUUIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var customModelTokenRE = regexp.MustCompile(`^cmv1_[A-Za-z0-9_-]{43}$`)
var customModelKeyRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var customModelIdentifierRE = regexp.MustCompile(`^openai/[A-Za-z0-9][A-Za-z0-9._:-]*$`)

const customModelsPath = "/management/v1/custom-models"
const maxCustomModels = 10000

var errCustomModelResponse = errors.New("Opper Management API returned an invalid or incomplete custom model response")

// localValidationError marks a rejected intent or request preparation failure
// before the HTTP client is called. Its message never contains submitted input.
type localValidationError struct{ message string }

func (e *localValidationError) Error() string { return e.message }

// IsLocalValidationError reports a definitive pre-request failure, including
// wrapped errors. Such failures cannot have committed an operation and must not
// be treated as an uncertain mutation requiring receipt recovery. Responses and
// transport failures are deliberately excluded, even if their outcome is known.
func IsLocalValidationError(err error) bool {
	var localErr *localValidationError
	return errors.As(err, &localErr)
}

func validCustomModelID(id string) bool {
	return customModelUUIDRE.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}
func customModelPath(id string) (string, error) {
	if !validCustomModelID(id) {
		return "", &localValidationError{message: "invalid custom model UUID"}
	}
	return customModelsPath + "/" + id, nil
}

// customModelRequest requires the exact documented success status. In
// particular, a legacy 200 acknowledgement must never count as guarded DELETE.
// No mutation is retried; uncertain outcomes belong to pure receipt recovery.
func (c *Client) customModelRequest(ctx context.Context, method, path string, body any, statuses ...int) ([]byte, int, error) {
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, 0, &localValidationError{message: "could not encode custom model request"}
		}
		input = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, input)
	if err != nil {
		return nil, 0, &localValidationError{message: "could not construct custom model request"}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, &requestError{cause: err}
	}
	defer resp.Body.Close()
	allowed := false
	for _, status := range statuses {
		if resp.StatusCode == status {
			allowed = true
		}
	}
	if !allowed {
		return nil, resp.StatusCode, &APIError{StatusCode: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, resp.StatusCode, &requestError{cause: err}
	}
	if len(data) > maxResponseBytes {
		return nil, resp.StatusCode, errors.New("Opper custom model response exceeded the size limit")
	}
	if method == http.MethodDelete && len(data) != 0 {
		return nil, resp.StatusCode, errCustomModelResponse
	}
	return data, resp.StatusCode, nil
}

// Exact response fields bound the secret-free contract. Duplicate fields and
// trailing JSON also fail closed instead of silently overwriting identity data.
func customModelObject(data []byte, fields ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return nil, errCustomModelResponse
	}
	result := make(map[string]json.RawMessage, len(fields))
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, errCustomModelResponse
		}
		key, ok := token.(string)
		if !ok {
			return nil, errCustomModelResponse
		}
		if _, duplicate := result[key]; duplicate {
			return nil, errCustomModelResponse
		}
		known := false
		for _, field := range fields {
			if key == field {
				known = true
			}
		}
		if !known {
			return nil, errCustomModelResponse
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, errCustomModelResponse
		}
		result[key] = raw
	}
	if token, err := dec.Token(); err != nil || token != json.Delim('}') || dec.Decode(new(any)) != io.EOF || len(result) != len(fields) {
		return nil, errCustomModelResponse
	}
	return result, nil
}

func customModelString(raw json.RawMessage) (string, error) {
	var value *string
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return "", errCustomModelResponse
	}
	return *value, nil
}
func customModelBool(raw json.RawMessage) (bool, error) {
	var value *bool
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return false, errCustomModelResponse
	}
	return *value, nil
}
func customModelInteger(raw json.RawMessage) (int, error) {
	var value *int
	if json.Unmarshal(raw, &value) != nil || value == nil || *value < 0 {
		return 0, errCustomModelResponse
	}
	return *value, nil
}

func decodeCustomModel(raw json.RawMessage) (CustomModel, error) {
	var model CustomModel
	obj, err := customModelObject(raw, "id", "name", "identifier", "provider", "type", "credential_present", "created_at", "updated_at")
	if err != nil {
		return model, err
	}
	for field, target := range map[string]*string{"id": &model.ID, "name": &model.Name, "identifier": &model.Identifier, "created_at": &model.CreatedAt, "updated_at": &model.UpdatedAt} {
		value, err := customModelString(obj[field])
		if err != nil {
			return CustomModel{}, err
		}
		*target = value
	}
	if !validCustomModelID(model.ID) {
		return CustomModel{}, errCustomModelResponse
	}
	for _, stamp := range []string{model.CreatedAt, model.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			return CustomModel{}, errCustomModelResponse
		}
	}
	if json.Unmarshal(obj["provider"], &model.Provider) != nil || json.Unmarshal(obj["type"], &model.Type) != nil {
		return CustomModel{}, errCustomModelResponse
	}
	model.CredentialPresent, err = customModelBool(obj["credential_present"])
	return model, err
}

func customModelEnvelope(raw []byte) (map[string]json.RawMessage, error) {
	return customModelObject(raw, "meta", "data")
}

func (c *Client) GetCustomModel(ctx context.Context, id string) (CustomModel, error) {
	path, err := customModelPath(id)
	if err != nil {
		return CustomModel{}, err
	}
	raw, _, err := c.customModelRequest(ctx, http.MethodGet, path, nil, 200)
	if err != nil {
		return CustomModel{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModel{}, err
	}
	if _, err := customModelObject(envelope["meta"]); err != nil {
		return CustomModel{}, err
	}
	model, err := decodeCustomModel(envelope["data"])
	if err == nil && model.ID != id {
		return CustomModel{}, errCustomModelResponse
	}
	return model, err
}

func (c *Client) GetCustomModelState(ctx context.Context, id string) (CustomModelState, error) {
	path, err := customModelPath(id)
	if err != nil {
		return CustomModelState{}, err
	}
	raw, _, err := c.customModelRequest(ctx, http.MethodGet, path+"/state", nil, 200)
	if err != nil {
		return CustomModelState{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModelState{}, err
	}
	meta, err := customModelObject(envelope["meta"], "change_token")
	if err != nil {
		return CustomModelState{}, err
	}
	token, err := customModelString(meta["change_token"])
	if err != nil || !customModelTokenRE.MatchString(token) {
		return CustomModelState{}, errCustomModelResponse
	}
	model, err := decodeCustomModel(envelope["data"])
	if err != nil || model.ID != id {
		return CustomModelState{}, errCustomModelResponse
	}
	return CustomModelState{Model: model, ChangeToken: token}, nil
}

func (c *Client) ListCustomModels(ctx context.Context) ([]CustomModel, error) {
	models := make([]CustomModel, 0)
	seen := make(map[string]bool)
	total := -1
	for offset := 0; ; {
		raw, _, err := c.customModelRequest(ctx, http.MethodGet, fmt.Sprintf("%s?offset=%d&limit=100", customModelsPath, offset), nil, 200)
		if err != nil {
			return nil, err
		}
		envelope, err := customModelEnvelope(raw)
		if err != nil {
			return nil, err
		}
		meta, err := customModelObject(envelope["meta"], "offset", "limit", "total_count")
		if err != nil {
			return nil, err
		}
		pageOffset, err := customModelInteger(meta["offset"])
		if err != nil || pageOffset != offset {
			return nil, errCustomModelResponse
		}
		limit, err := customModelInteger(meta["limit"])
		if err != nil || limit != 100 {
			return nil, errCustomModelResponse
		}
		count, err := customModelInteger(meta["total_count"])
		if err != nil || count > maxCustomModels || total >= 0 && total != count {
			return nil, errCustomModelResponse
		}
		total = count
		var rows *[]json.RawMessage
		if json.Unmarshal(envelope["data"], &rows) != nil || rows == nil || len(*rows) > limit || offset+len(*rows) > total {
			return nil, errCustomModelResponse
		}
		for _, row := range *rows {
			model, err := decodeCustomModel(row)
			if err != nil || seen[model.ID] {
				return nil, errCustomModelResponse
			}
			seen[model.ID] = true
			models = append(models, model)
		}
		offset += len(*rows)
		if offset == total {
			break
		}
		if len(*rows) == 0 {
			return nil, errCustomModelResponse
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func customModelNameValid(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 255
}
func customModelIdentifierValid(value string) bool {
	return len(value) <= 255 && customModelIdentifierRE.MatchString(value)
}
func customModelCredentialValid(value string) bool {
	return utf8.ValidString(value) && len(value) <= 8192 && strings.TrimSpace(value) != ""
}

func decodeCreationReceipt(raw json.RawMessage, key string) (CustomModelCreationReceipt, error) {
	obj, err := customModelObject(raw, "idempotency_key", "custom_model_uuid", "resource_exists")
	if err != nil {
		return CustomModelCreationReceipt{}, err
	}
	k, err := customModelString(obj["idempotency_key"])
	if err != nil || k != key {
		return CustomModelCreationReceipt{}, errCustomModelResponse
	}
	id, err := customModelString(obj["custom_model_uuid"])
	if err != nil || !validCustomModelID(id) {
		return CustomModelCreationReceipt{}, errCustomModelResponse
	}
	exists, err := customModelBool(obj["resource_exists"])
	if err != nil {
		return CustomModelCreationReceipt{}, err
	}
	return CustomModelCreationReceipt{IdempotencyKey: k, CustomModelUUID: id, ResourceExists: exists}, nil
}

func (c *Client) CreateCustomModel(ctx context.Context, name, identifier, credential, operationKey string) (CustomModelCreationResult, error) {
	if !customModelNameValid(name) || !customModelIdentifierValid(identifier) || !customModelCredentialValid(credential) || !customModelKeyRE.MatchString(operationKey) {
		return CustomModelCreationResult{}, &localValidationError{message: "invalid custom model creation intent"}
	}
	raw, status, err := c.customModelRequest(ctx, http.MethodPost, customModelsPath, map[string]string{"name": name, "identifier": identifier, "credential": credential, "idempotency_key": operationKey}, 201, 200)
	if err != nil {
		return CustomModelCreationResult{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModelCreationResult{}, err
	}
	meta, err := customModelObject(envelope["meta"], "operation", "idempotency_replayed")
	if err != nil {
		return CustomModelCreationResult{}, err
	}
	replayed, err := customModelBool(meta["idempotency_replayed"])
	if err != nil || replayed != (status == 200) {
		return CustomModelCreationResult{}, errCustomModelResponse
	}
	receipt, err := decodeCreationReceipt(meta["operation"], operationKey)
	if err != nil || !receipt.ResourceExists {
		return CustomModelCreationResult{}, errCustomModelResponse
	}
	model, err := decodeCustomModel(envelope["data"])
	if err != nil || model.ID != receipt.CustomModelUUID {
		return CustomModelCreationResult{}, errCustomModelResponse
	}
	if !replayed && (model.Name != name || model.Identifier != identifier || !model.CredentialPresent || model.Provider == nil || *model.Provider != "openai" || model.Type == nil || *model.Type != "llm") {
		return CustomModelCreationResult{}, errCustomModelResponse
	}
	return CustomModelCreationResult{Model: model, Operation: receipt, Replayed: replayed}, nil
}

func (c *Client) GetCustomModelCreation(ctx context.Context, operationKey string) (CustomModelCreationReceipt, error) {
	if !customModelKeyRE.MatchString(operationKey) {
		return CustomModelCreationReceipt{}, &localValidationError{message: "invalid custom model creation operation key"}
	}
	raw, _, err := c.customModelRequest(ctx, http.MethodGet, customModelsPath+"/operations/"+operationKey, nil, 200)
	if err != nil {
		return CustomModelCreationReceipt{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModelCreationReceipt{}, err
	}
	if _, err := customModelObject(envelope["meta"]); err != nil {
		return CustomModelCreationReceipt{}, err
	}
	return decodeCreationReceipt(envelope["data"], operationKey)
}

func decodeUpdateReceipt(raw json.RawMessage, key string) (CustomModelUpdateReceipt, error) {
	obj, err := customModelObject(raw, "idempotency_key", "custom_model_uuid", "resource_exists", "result_matches_current_state")
	if err != nil {
		return CustomModelUpdateReceipt{}, err
	}
	k, err := customModelString(obj["idempotency_key"])
	if err != nil || k != key {
		return CustomModelUpdateReceipt{}, errCustomModelResponse
	}
	id, err := customModelString(obj["custom_model_uuid"])
	if err != nil || !validCustomModelID(id) {
		return CustomModelUpdateReceipt{}, errCustomModelResponse
	}
	exists, err := customModelBool(obj["resource_exists"])
	if err != nil {
		return CustomModelUpdateReceipt{}, err
	}
	var matches *bool
	if json.Unmarshal(obj["result_matches_current_state"], &matches) != nil || !exists && (matches == nil || *matches) {
		return CustomModelUpdateReceipt{}, errCustomModelResponse
	}
	return CustomModelUpdateReceipt{IdempotencyKey: k, CustomModelUUID: id, ResourceExists: exists, ResultMatchesCurrentState: matches}, nil
}

func (c *Client) UpdateCustomModel(ctx context.Context, id string, input CustomModelUpdateInput) (CustomModelUpdateResult, error) {
	path, err := customModelPath(id)
	if err != nil {
		return CustomModelUpdateResult{}, err
	}
	if !customModelTokenRE.MatchString(input.ExpectedChangeToken) || !customModelKeyRE.MatchString(input.IdempotencyKey) || input.Name == nil && input.Identifier == nil && input.Credential == nil || input.Name != nil && !customModelNameValid(*input.Name) || input.Identifier != nil && !customModelIdentifierValid(*input.Identifier) || input.Credential != nil && !customModelCredentialValid(*input.Credential) {
		return CustomModelUpdateResult{}, &localValidationError{message: "invalid custom model update intent"}
	}
	body := map[string]string{"expected_change_token": input.ExpectedChangeToken, "idempotency_key": input.IdempotencyKey}
	if input.Name != nil {
		body["name"] = *input.Name
	}
	if input.Identifier != nil {
		body["identifier"] = *input.Identifier
	}
	if input.Credential != nil {
		body["credential"] = *input.Credential
	}
	raw, _, err := c.customModelRequest(ctx, http.MethodPatch, path, body, 200)
	if err != nil {
		return CustomModelUpdateResult{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModelUpdateResult{}, err
	}
	meta, err := customModelObject(envelope["meta"], "idempotency_replayed")
	if err != nil {
		return CustomModelUpdateResult{}, err
	}
	replayed, err := customModelBool(meta["idempotency_replayed"])
	if err != nil {
		return CustomModelUpdateResult{}, err
	}
	receipt, err := decodeUpdateReceipt(envelope["data"], input.IdempotencyKey)
	if err != nil || receipt.CustomModelUUID != id {
		return CustomModelUpdateResult{}, errCustomModelResponse
	}
	if !replayed && (!receipt.ResourceExists || receipt.ResultMatchesCurrentState == nil || !*receipt.ResultMatchesCurrentState) {
		return CustomModelUpdateResult{}, errCustomModelResponse
	}
	return CustomModelUpdateResult{Operation: receipt, Replayed: replayed}, nil
}

func (c *Client) GetCustomModelUpdateOperation(ctx context.Context, operationKey string) (CustomModelUpdateReceipt, error) {
	if !customModelKeyRE.MatchString(operationKey) {
		return CustomModelUpdateReceipt{}, &localValidationError{message: "invalid custom model update operation key"}
	}
	raw, _, err := c.customModelRequest(ctx, http.MethodGet, customModelsPath+"/update-operations/"+operationKey, nil, 200)
	if err != nil {
		return CustomModelUpdateReceipt{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModelUpdateReceipt{}, err
	}
	if _, err := customModelObject(envelope["meta"]); err != nil {
		return CustomModelUpdateReceipt{}, err
	}
	return decodeUpdateReceipt(envelope["data"], operationKey)
}

func (c *Client) DeleteCustomModel(ctx context.Context, id, expectedToken string) error {
	path, err := customModelPath(id)
	if err != nil {
		return err
	}
	if !customModelTokenRE.MatchString(expectedToken) {
		return &localValidationError{message: "invalid custom model deletion precondition"}
	}
	_, _, err = c.customModelRequest(ctx, http.MethodDelete, path, map[string]string{"expected_change_token": expectedToken}, 204)
	return err
}
