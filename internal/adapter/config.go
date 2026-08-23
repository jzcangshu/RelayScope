package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ApplyConfigDefaults reads the "default" values declared in a JSON Schema's
// properties and injects them into raw for any key that is absent. This makes
// the schema the single source of default values, eliminating the need for
// hand-written struct-init defaults in each adapter's Collect method.
//
// Keys present in raw (including those with null or empty-string values) are
// left untouched so that explicit user input is respected. Unknown keys are
// preserved. No jsonschema dependency is introduced.
func ApplyConfigDefaults(schema, raw json.RawMessage) (json.RawMessage, error) {
	var schemaObj map[string]any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		return nil, fmt.Errorf("parse config schema: %w", err)
	}

	properties, ok := schemaObj["properties"].(map[string]any)
	if !ok {
		if len(bytes.TrimSpace(raw)) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return raw, nil
	}

	config := make(map[string]any)
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, fmt.Errorf("parse config JSON: %w", err)
		}
	}
	if config == nil {
		config = make(map[string]any)
	}

	for key, propDef := range properties {
		if _, exists := config[key]; exists {
			continue
		}
		if propMap, ok := propDef.(map[string]any); ok {
			if defaultValue, hasDefault := propMap["default"]; hasDefault {
				config[key] = defaultValue
			}
		}
	}

	return json.Marshal(config)
}
