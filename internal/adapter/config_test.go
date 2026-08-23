package adapter

import (
	"encoding/json"
	"testing"
)

func TestApplyConfigDefaults(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","default":"/api/data"},"hours":{"type":"integer","default":24},"enabled":{"type":"boolean","default":false},"optional":{"type":"string"}}}`)

	tests := []struct {
		name    string
		raw     json.RawMessage
		want    map[string]any
		wantErr bool
	}{
		{
			name: "empty raw gets all defaults",
			raw:  json.RawMessage(``),
			want: map[string]any{"path": "/api/data", "hours": float64(24), "enabled": false},
		},
		{
			name: "null raw gets all defaults",
			raw:  json.RawMessage(`null`),
			want: map[string]any{"path": "/api/data", "hours": float64(24), "enabled": false},
		},
		{
			name: "partial override keeps user values",
			raw:  json.RawMessage(`{"path":"/custom"}`),
			want: map[string]any{"path": "/custom", "hours": float64(24), "enabled": false},
		},
		{
			name: "explicit empty string preserved",
			raw:  json.RawMessage(`{"path":""}`),
			want: map[string]any{"path": "", "hours": float64(24), "enabled": false},
		},
		{
			name: "explicit zero preserved",
			raw:  json.RawMessage(`{"hours":0}`),
			want: map[string]any{"path": "/api/data", "hours": float64(0), "enabled": false},
		},
		{
			name: "unknown keys preserved",
			raw:  json.RawMessage(`{"extra":"kept"}`),
			want: map[string]any{"path": "/api/data", "hours": float64(24), "enabled": false, "extra": "kept"},
		},
		{
			name: "property without default stays absent",
			raw:  json.RawMessage(`{}`),
			want: map[string]any{"path": "/api/data", "hours": float64(24), "enabled": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ApplyConfigDefaults(schema, tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			for key, wantVal := range tt.want {
				gotVal, exists := got[key]
				if !exists {
					t.Fatalf("key %q missing from result", key)
				}
				if gotVal != wantVal {
					t.Fatalf("key %q = %v, want %v", key, gotVal, wantVal)
				}
			}
			for key := range got {
				if _, expected := tt.want[key]; !expected {
					t.Fatalf("unexpected key %q in result", key)
				}
			}
		})
	}
}

func TestApplyConfigDefaultsNoProperties(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	result, err := ApplyConfigDefaults(schema, json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["x"] != float64(1) {
		t.Fatalf("x = %v, want 1", got["x"])
	}
}

func TestApplyConfigDefaultsInvalidRaw(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	_, err := ApplyConfigDefaults(schema, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestApplyConfigDefaultsValidatesKnownProperties(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["fast","safe"]},"count":{"type":"integer","minimum":1,"maximum":3},"enabled":{"type":"boolean"}}}`)
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"enum", `{"mode":"other"}`},
		{"type", `{"enabled":"yes"}`},
		{"minimum", `{"count":0}`},
		{"maximum", `{"count":4}`},
		{"integer", `{"count":1.5}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ApplyConfigDefaults(schema, json.RawMessage(test.raw)); err == nil {
				t.Fatal("expected schema validation error")
			}
		})
	}
	if _, err := ApplyConfigDefaults(schema, json.RawMessage(`{"mode":"safe","count":2,"enabled":true,"future":"kept"}`)); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
