package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The public spec already exceeds the ordinary 1 MiB resource response limit.
// Keep its larger allowance isolated to this discovery read.
const maxAPIKeyOpenAPIBytes = 8 * 1024 * 1024

var errAPIKeyAllowedIPsUnsupported = errors.New("Opper backend does not advertise project API key IP allowlist creation and updates; no API key creation was attempted")

func (c *Client) requireAPIKeyAllowedIPs(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v3/openapi.json", nil)
	if err != nil {
		return errAPIKeyAllowedIPsUnsupported
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("API key IP allowlist discovery failed; no creation was attempted: %w", &requestError{cause: err})
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("API key IP allowlist discovery failed; no creation was attempted: %w", &APIError{StatusCode: response.StatusCode})
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAPIKeyOpenAPIBytes+1))
	if err != nil {
		return fmt.Errorf("API key IP allowlist discovery failed; no creation was attempted: %w", &requestError{cause: err})
	}
	var document map[string]any
	if len(raw) > maxAPIKeyOpenAPIBytes || json.Unmarshal(raw, &document) != nil {
		return errAPIKeyAllowedIPsUnsupported
	}
	version, ok := document["openapi"].(string)
	if !ok || !strings.HasPrefix(version, "3.") || !allowedIPsOperation(document, "/management/v1/projects/{uuid}/api-keys", "post", false) || !allowedIPsOperation(document, "/management/v1/projects/{uuid}/api-keys/{id}", "patch", true) {
		return errAPIKeyAllowedIPsUnsupported
	}
	return nil
}

func allowedIPsOperation(document map[string]any, path, method string, required bool) bool {
	paths, _ := document["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)
	operation, _ := item[method].(map[string]any)
	body, ok := resolveAllowedIPsSchema(document, operation["requestBody"])
	if !ok {
		return false
	}
	content, _ := body["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, ok := resolveAllowedIPsSchema(document, media["schema"])
	if !ok || schema["type"] != "object" {
		return false
	}
	properties, _ := schema["properties"].(map[string]any)
	list, ok := resolveAllowedIPsSchema(document, properties["allowed_ips"])
	if !ok || list["type"] != "array" {
		return false
	}
	entry, ok := resolveAllowedIPsSchema(document, list["items"])
	if !ok || entry["type"] != "string" {
		return false
	}
	if required {
		fields, _ := schema["required"].([]any)
		for _, field := range fields {
			if field == "allowed_ips" {
				return true
			}
		}
		return false
	}
	return true
}

func resolveAllowedIPsSchema(document map[string]any, node any) (map[string]any, bool) {
	seen := map[string]bool{}
	for depth := 0; depth < 32; depth++ {
		object, ok := node.(map[string]any)
		if !ok {
			return nil, false
		}
		reference, exists := object["$ref"]
		if !exists {
			return object, true
		}
		ref, ok := reference.(string)
		if !ok || !strings.HasPrefix(ref, "#/") || seen[ref] {
			return nil, false
		}
		for key := range object {
			if key != "$ref" && key != "description" && key != "summary" && key != "title" {
				return nil, false
			}
		}
		seen[ref] = true
		node = document
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			value, ok := node.(map[string]any)
			if !ok {
				return nil, false
			}
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			node = value[part]
		}
	}
	return nil, false
}
