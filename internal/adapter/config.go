package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
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
	if err := validateConfig(properties, config); err != nil {
		return nil, err
	}

	return json.Marshal(config)
}

func validateConfig(properties map[string]any, config map[string]any) error {
	for key, value := range config {
		propDef, known := properties[key]
		if !known {
			continue
		}
		prop, ok := propDef.(map[string]any)
		if !ok {
			continue
		}
		if expected, ok := prop["type"].(string); ok && !jsonTypeMatches(expected, value) {
			return fmt.Errorf("config field %q must be %s", key, expected)
		}
		if enum, ok := prop["enum"].([]any); ok {
			matched := false
			for _, allowed := range enum {
				if reflect.DeepEqual(value, allowed) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("config field %q has an unsupported value", key)
			}
		}
		if number, ok := value.(float64); ok {
			if minimum, ok := prop["minimum"].(float64); ok && number < minimum {
				return fmt.Errorf("config field %q must be at least %g", key, minimum)
			}
			if maximum, ok := prop["maximum"].(float64); ok && number > maximum {
				return fmt.Errorf("config field %q must be at most %g", key, maximum)
			}
		}
	}
	return nil
}

func jsonTypeMatches(expected string, value any) bool {
	if value == nil {
		return expected == "null"
	}
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return true
	}
}
