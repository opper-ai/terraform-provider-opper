package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var errAPIKeyResponse = errors.New("Opper Management API returned an invalid or incomplete API key response")

// Decode policy independently so an unconfirmed restriction cannot discard the
// minted key's otherwise valid identity and one-time secret.
func decodeAPIKeyEnvelope(raw []byte, keepSecret bool) (APIKey, error) {
	envelope, err := apiKeyObject(raw)
	if err != nil {
		return APIKey{}, errAPIKeyResponse
	}
	key, keyErr := decodeRuntimeAPIKey(envelope["data"], keepSecret)
	if meta := envelope["meta"]; len(meta) > 0 && !bytes.Equal(bytes.TrimSpace(meta), []byte("null")) {
		fields, err := apiKeyObject(meta)
		if err != nil {
			return key, errAPIKeyResponse
		}
		if replay := fields["idempotency_replayed"]; len(replay) > 0 && json.Unmarshal(replay, &key.IdempotencyReplayed) != nil {
			return key, errAPIKeyResponse
		}
	}
	return key, keyErr
}

func decodeRuntimeAPIKey(raw []byte, keepSecret bool) (APIKey, error) {
	if _, err := apiKeyObject(raw); err != nil {
		return APIKey{}, errAPIKeyResponse
	}
	var wire struct {
		APIKey
		AllowedIPs json.RawMessage `json:"allowed_ips"`
		Key        json.RawMessage `json:"key"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.ID <= 0 {
		return APIKey{}, errAPIKeyResponse
	}
	key := wire.APIKey
	if keepSecret && len(wire.Key) > 0 && json.Unmarshal(wire.Key, &key.Key) != nil {
		return key, errAPIKeyResponse
	}
	if len(wire.AllowedIPs) == 0 || bytes.Equal(bytes.TrimSpace(wire.AllowedIPs), []byte("null")) {
		return key, nil
	}
	var ips []string
	if err := json.Unmarshal(wire.AllowedIPs, &ips); err != nil || ips == nil {
		return key, errAPIKeyResponse
	}
	var err error
	key.AllowedIPs, err = NormalizeAllowedIPs(ips)
	if err != nil {
		return key, errAPIKeyResponse
	}
	return key, nil
}

func apiKeyObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errAPIKeyResponse
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, errAPIKeyResponse
		}
		if _, exists := fields[name]; exists {
			return nil, errAPIKeyResponse
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, errAPIKeyResponse
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || decoder.Decode(new(any)) != io.EOF {
		return nil, errAPIKeyResponse
	}
	return fields, nil
}
