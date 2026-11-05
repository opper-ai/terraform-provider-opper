package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// CustomModelCreationStateReceipt retains the complete original definition's
// validator, captured in the creation transaction. It is historical proof:
// later edits, deletion and server signing-key rotation do not replace it.
type CustomModelCreationStateReceipt struct {
	IdempotencyKey     string `json:"idempotency_key"`
	CustomModelUUID    string `json:"custom_model_uuid"`
	ResourceExists     bool   `json:"resource_exists"`
	InitialChangeToken string `json:"initial_change_token"`
}

type CustomModelCreationStateResult struct {
	Model     CustomModel
	Operation CustomModelCreationStateReceipt
	Replayed  bool
}

const customModelCreationsPath = customModelsPath + "/creations"

func decodeCreationStateReceipt(raw json.RawMessage, key string) (CustomModelCreationStateReceipt, error) {
	obj, err := customModelObject(raw, "idempotency_key", "custom_model_uuid", "resource_exists", "initial_change_token")
	if err != nil {
		return CustomModelCreationStateReceipt{}, err
	}
	k, err := customModelString(obj["idempotency_key"])
	if err != nil || k != key {
		return CustomModelCreationStateReceipt{}, errCustomModelResponse
	}
	id, err := customModelString(obj["custom_model_uuid"])
	if err != nil || !validCustomModelID(id) {
		return CustomModelCreationStateReceipt{}, errCustomModelResponse
	}
	exists, err := customModelBool(obj["resource_exists"])
	if err != nil {
		return CustomModelCreationStateReceipt{}, err
	}
	token, err := customModelString(obj["initial_change_token"])
	if err != nil || !customModelTokenRE.MatchString(token) {
		return CustomModelCreationStateReceipt{}, errCustomModelResponse
	}
	return CustomModelCreationStateReceipt{IdempotencyKey: k, CustomModelUUID: id, ResourceExists: exists, InitialChangeToken: token}, nil
}

// CreateCustomModelWithState uses only the opt-in endpoint which commits an
// original complete-state validator with creation. It never falls back to the
// older metadata-only creation endpoint, including on an old replica's 404.
func (c *Client) CreateCustomModelWithState(ctx context.Context, name, identifier, credential, operationKey string) (CustomModelCreationStateResult, error) {
	if !customModelNameValid(name) || !customModelIdentifierValid(identifier) || !customModelCredentialValid(credential) || !customModelKeyRE.MatchString(operationKey) {
		return CustomModelCreationStateResult{}, &localValidationError{message: "invalid custom model creation intent"}
	}
	raw, status, err := c.customModelRequest(ctx, http.MethodPost, customModelCreationsPath, map[string]string{"name": name, "identifier": identifier, "credential": credential, "idempotency_key": operationKey}, 201, 200)
	if err != nil {
		return CustomModelCreationStateResult{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModelCreationStateResult{}, err
	}
	meta, err := customModelObject(envelope["meta"], "operation", "idempotency_replayed")
	if err != nil {
		return CustomModelCreationStateResult{}, err
	}
	replayed, err := customModelBool(meta["idempotency_replayed"])
	if err != nil || replayed != (status == 200) {
		return CustomModelCreationStateResult{}, errCustomModelResponse
	}
	receipt, err := decodeCreationStateReceipt(meta["operation"], operationKey)
	if err != nil || !receipt.ResourceExists {
		return CustomModelCreationStateResult{}, errCustomModelResponse
	}
	model, err := decodeCustomModel(envelope["data"])
	if err != nil || model.ID != receipt.CustomModelUUID {
		return CustomModelCreationStateResult{}, errCustomModelResponse
	}
	if !replayed && (model.Name != name || model.Identifier != identifier || !model.CredentialPresent || model.Provider == nil || *model.Provider != "openai" || model.Type == nil || *model.Type != "llm") {
		return CustomModelCreationStateResult{}, errCustomModelResponse
	}
	return CustomModelCreationStateResult{Model: model, Operation: receipt, Replayed: replayed}, nil
}

// GetCustomModelCreationState is a pure actor-bound receipt read. The returned
// initial token remains original even when stale; callers must compare it with
// state rather than silently adopting a new credential or configuration.
func (c *Client) GetCustomModelCreationState(ctx context.Context, operationKey string) (CustomModelCreationStateReceipt, error) {
	if !customModelKeyRE.MatchString(operationKey) {
		return CustomModelCreationStateReceipt{}, &localValidationError{message: "invalid custom model creation operation key"}
	}
	raw, _, err := c.customModelRequest(ctx, http.MethodGet, customModelCreationsPath+"/"+operationKey, nil, 200)
	if err != nil {
		return CustomModelCreationStateReceipt{}, err
	}
	envelope, err := customModelEnvelope(raw)
	if err != nil {
		return CustomModelCreationStateReceipt{}, err
	}
	if _, err := customModelObject(envelope["meta"]); err != nil {
		return CustomModelCreationStateReceipt{}, err
	}
	return decodeCreationStateReceipt(envelope["data"], operationKey)
}
