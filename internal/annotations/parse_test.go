package annotations

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestParseToolDefinition(t *testing.T) {
	// Build raw bytes for ToolDefinition{name: "test_tool", description: "A test", strict: true, auto_execute: true}
	var buf []byte
	// field 1 (name) = "test_tool"
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendString(buf, "test_tool")
	// field 2 (description) = "A test"
	buf = protowire.AppendTag(buf, 2, protowire.BytesType)
	buf = protowire.AppendString(buf, "A test")
	// field 3 (strict) = true
	buf = protowire.AppendTag(buf, 3, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 1)
	// field 4 (auto_execute) = true
	buf = protowire.AppendTag(buf, 4, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 1)

	td := parseToolDefinition(buf)

	if td.Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got '%s'", td.Name)
	}
	if td.Description != "A test" {
		t.Errorf("expected description 'A test', got '%s'", td.Description)
	}
	if !td.Strict {
		t.Error("expected strict to be true")
	}
	if !td.AutoExecute {
		t.Error("expected auto_execute to be true")
	}
}

func TestParseToolDefinition_NoStrictNoAutoExecute(t *testing.T) {
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendString(buf, "simple")
	buf = protowire.AppendTag(buf, 2, protowire.BytesType)
	buf = protowire.AppendString(buf, "Simple tool")

	td := parseToolDefinition(buf)

	if td.Name != "simple" {
		t.Errorf("expected name 'simple', got '%s'", td.Name)
	}
	if td.Description != "Simple tool" {
		t.Errorf("expected description 'Simple tool', got '%s'", td.Description)
	}
	if td.Strict {
		t.Error("expected strict to be false by default")
	}
	if td.AutoExecute {
		t.Error("expected auto_execute to be false by default")
	}
}

func TestParseToolDefinition_EmptyBytes(t *testing.T) {
	td := parseToolDefinition(nil)

	if td.Name != "" {
		t.Errorf("expected empty name, got '%s'", td.Name)
	}
	if td.Description != "" {
		t.Errorf("expected empty description, got '%s'", td.Description)
	}
	if td.Strict {
		t.Error("expected strict to be false")
	}
	if td.AutoExecute {
		t.Error("expected auto_execute to be false")
	}
}

func TestParseFieldContextDescription(t *testing.T) {
	var buf []byte
	// field 1 (description) = "The link destination URL"
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendString(buf, "The link destination URL")

	desc := parseFieldContextDescription(buf)

	if desc != "The link destination URL" {
		t.Errorf("expected 'The link destination URL', got '%s'", desc)
	}
}

func TestParseFieldContextDescription_Empty(t *testing.T) {
	desc := parseFieldContextDescription(nil)

	if desc != "" {
		t.Errorf("expected empty string, got '%s'", desc)
	}
}

func TestFindExtension(t *testing.T) {
	// Build a buffer with two length-delimited extensions at different field numbers.
	var buf []byte

	// Extension at field 52103 (fieldContextField) with content "hello"
	buf = protowire.AppendTag(buf, 52103, protowire.BytesType)
	buf = protowire.AppendBytes(buf, []byte("hello"))

	// Extension at field 52104 (rpcToolField) with content "world"
	buf = protowire.AppendTag(buf, 52104, protowire.BytesType)
	buf = protowire.AppendBytes(buf, []byte("world"))

	ext := findExtension(buf, 52104)
	if string(ext) != "world" {
		t.Errorf("expected 'world', got '%s'", string(ext))
	}

	ext = findExtension(buf, 52103)
	if string(ext) != "hello" {
		t.Errorf("expected 'hello', got '%s'", string(ext))
	}

	ext = findExtension(buf, 99999)
	if ext != nil {
		t.Errorf("expected nil for missing extension, got '%s'", string(ext))
	}
}

func TestFindExtension_EmptyBytes(t *testing.T) {
	ext := findExtension(nil, 52104)
	if ext != nil {
		t.Error("expected nil for empty input")
	}
}

func TestHasFieldBehaviorValue(t *testing.T) {
	// Build buffer with field_behavior = OUTPUT_ONLY (value 3) at field 1052
	var buf []byte
	buf = protowire.AppendTag(buf, 1052, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 3)

	if !hasFieldBehaviorValue(buf, 3) {
		t.Error("expected OUTPUT_ONLY (3) to be found")
	}
	if hasFieldBehaviorValue(buf, 1) {
		t.Error("expected REQUIRED (1) not to be found")
	}
}

func TestHasFieldBehaviorValue_Packed(t *testing.T) {
	// Build packed repeated field_behavior with values [1, 3]
	var packed []byte
	packed = protowire.AppendVarint(packed, 1) // REQUIRED
	packed = protowire.AppendVarint(packed, 3) // OUTPUT_ONLY

	var buf []byte
	buf = protowire.AppendTag(buf, 1052, protowire.BytesType)
	buf = protowire.AppendBytes(buf, packed)

	if !hasFieldBehaviorValue(buf, 3) {
		t.Error("expected OUTPUT_ONLY (3) to be found in packed field")
	}
	if !hasFieldBehaviorValue(buf, 1) {
		t.Error("expected REQUIRED (1) to be found in packed field")
	}
	if hasFieldBehaviorValue(buf, 5) {
		t.Error("expected value 5 not to be found in packed field")
	}
}

func TestHasFieldBehaviorValue_Empty(t *testing.T) {
	if hasFieldBehaviorValue(nil, 3) {
		t.Error("expected false for empty input")
	}
}

func TestConsumeField(t *testing.T) {
	tests := []struct {
		name string
		typ  protowire.Type
		data []byte
	}{
		{
			name: "varint",
			typ:  protowire.VarintType,
			data: protowire.AppendVarint(nil, 42),
		},
		{
			name: "fixed32",
			typ:  protowire.Fixed32Type,
			data: protowire.AppendFixed32(nil, 42),
		},
		{
			name: "fixed64",
			typ:  protowire.Fixed64Type,
			data: protowire.AppendFixed64(nil, 42),
		},
		{
			name: "bytes",
			typ:  protowire.BytesType,
			data: protowire.AppendBytes(nil, []byte("test")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := consumeField(tt.typ, tt.data)
			if n < 0 {
				t.Errorf("consumeField returned negative for %s", tt.name)
			}
			if n != len(tt.data) {
				t.Errorf("consumeField(%s) = %d, want %d", tt.name, n, len(tt.data))
			}
		})
	}
}

func TestConsumeField_UnknownType(t *testing.T) {
	n := consumeField(protowire.Type(99), []byte{0x00})
	if n != -1 {
		t.Errorf("expected -1 for unknown type, got %d", n)
	}
}
