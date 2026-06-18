package controlplane

import (
	"bytes"
	"encoding/json"
)

func mintConversationJSONFieldPresent(fields map[string]json.RawMessage, name string) bool {
	if fields == nil {
		return false
	}
	raw, ok := fields[name]
	if !ok {
		return false
	}
	return !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func mintConversationJSONFieldIsArray(raw json.RawMessage) bool {
	return bytes.HasPrefix(bytes.TrimSpace(raw), []byte("["))
}
