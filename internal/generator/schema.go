package generator

import (
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/Loschcode/protoc-gen-ai-tools/internal/annotations"
)

// SchemaGenerator converts proto message descriptors into JSON Schema maps.
type SchemaGenerator struct {
	visited map[protoreflect.FullName]bool
	strict  bool
}

// NewSchemaGenerator creates a generator. When strict is true, every object
// level gets "additionalProperties": false.
func NewSchemaGenerator(strict bool) *SchemaGenerator {
	return &SchemaGenerator{
		visited: make(map[protoreflect.FullName]bool),
		strict:  strict,
	}
}

// Generate produces a JSON Schema map for the given message descriptor.
func (sg *SchemaGenerator) Generate(msg protoreflect.MessageDescriptor) map[string]any {
	// Handle well-known types at the top level too.
	if wkt := sg.wellKnownType(msg); wkt != nil {
		return wkt
	}

	return sg.messageSchema(msg)
}

func (sg *SchemaGenerator) messageSchema(msg protoreflect.MessageDescriptor) map[string]any {
	// Guard against infinite recursion.
	fullName := msg.FullName()
	if sg.visited[fullName] {
		return map[string]any{"type": "object"}
	}
	sg.visited[fullName] = true
	defer func() { sg.visited[fullName] = false }()

	properties := make(map[string]any)
	var required []string

	fields := msg.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)

		// Skip OUTPUT_ONLY fields and tool-skipped fields.
		if annotations.IsOutputOnly(field) || annotations.IsToolSkipped(field) {
			continue
		}

		name := string(field.Name())
		schema := sg.fieldSchema(field)
		isOptional := field.HasOptionalKeyword() || field.ContainingOneof() != nil

		// Add description from field_context annotation or proto comments.
		desc := annotations.FieldDescription(field)
		if desc == "" {
			si := field.ParentFile().SourceLocations().ByDescriptor(field)
			desc = strings.TrimSpace(si.LeadingComments)
		}
		if desc != "" {
			schema["description"] = desc
		}

		// In strict mode, optional fields are nullable via anyOf union.
		if sg.strict && isOptional {
			properties[name] = map[string]any{
				"anyOf": []any{schema, map[string]any{"type": "null"}},
			}
			// Preserve description on the wrapper
			if desc != "" {
				properties[name].(map[string]any)["description"] = desc
				delete(schema, "description")
			}
		} else {
			properties[name] = schema
		}

		if sg.strict {
			// OpenAI strict mode: ALL properties must be in required.
			required = append(required, name)
		} else if !isOptional {
			required = append(required, name)
		}
	}

	result := map[string]any{
		"type":       "object",
		"properties": properties,
	}

	if len(required) > 0 {
		result["required"] = required
	}

	if sg.strict {
		result["additionalProperties"] = false
	}

	return result
}

func (sg *SchemaGenerator) fieldSchema(field protoreflect.FieldDescriptor) map[string]any {
	// Map fields: map<K,V> → {"type": "object", "additionalProperties": <V>}
	// Note: map fields are incompatible with strict mode. Use
	// (ai.tools.v1.tool_field).skip = true on map fields when strict is on.
	if field.IsMap() {
		valueField := field.MapValue()
		valueSchema := sg.singularSchema(valueField)
		return map[string]any{
			"type":                 "object",
			"additionalProperties": valueSchema,
		}
	}

	// Repeated fields: repeated X → {"type": "array", "items": <X>}
	if field.IsList() {
		itemSchema := sg.singularSchema(field)
		return map[string]any{
			"type":  "array",
			"items": itemSchema,
		}
	}

	return sg.singularSchema(field)
}

func (sg *SchemaGenerator) singularSchema(field protoreflect.FieldDescriptor) map[string]any {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}

	case protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Fixed32Kind, protoreflect.Fixed64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return map[string]any{"type": "integer"}

	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}

	case protoreflect.StringKind:
		return map[string]any{"type": "string"}

	case protoreflect.BytesKind:
		return map[string]any{"type": "string", "format": "byte"}

	case protoreflect.EnumKind:
		return sg.enumSchema(field.Enum())

	case protoreflect.MessageKind, protoreflect.GroupKind:
		return sg.nestedMessageSchema(field.Message())

	default:
		return map[string]any{"type": "string"}
	}
}

func (sg *SchemaGenerator) enumSchema(enum protoreflect.EnumDescriptor) map[string]any {
	var values []any
	for i := 0; i < enum.Values().Len(); i++ {
		val := enum.Values().Get(i)
		// Skip _UNSPECIFIED = 0 values.
		if val.Number() == 0 {
			continue
		}
		values = append(values, string(val.Name()))
	}
	result := map[string]any{
		"type": "string",
		"enum": values,
	}
	return result
}

func (sg *SchemaGenerator) nestedMessageSchema(msg protoreflect.MessageDescriptor) map[string]any {
	if wkt := sg.wellKnownType(msg); wkt != nil {
		return wkt
	}
	return sg.messageSchema(msg)
}

func (sg *SchemaGenerator) wellKnownType(msg protoreflect.MessageDescriptor) map[string]any {
	switch msg.FullName() {
	case "google.protobuf.Timestamp":
		return map[string]any{"type": "string", "format": "date-time"}
	case "google.protobuf.Empty":
		return map[string]any{"type": "object"}
	case "google.protobuf.Struct":
		return map[string]any{"type": "object"}
	case "google.protobuf.Value":
		return map[string]any{}
	case "google.protobuf.StringValue":
		return map[string]any{"type": "string"}
	case "google.protobuf.Int32Value", "google.protobuf.Int64Value",
		"google.protobuf.UInt32Value", "google.protobuf.UInt64Value":
		return map[string]any{"type": "integer"}
	case "google.protobuf.FloatValue", "google.protobuf.DoubleValue":
		return map[string]any{"type": "number"}
	case "google.protobuf.BoolValue":
		return map[string]any{"type": "boolean"}
	case "google.protobuf.BytesValue":
		return map[string]any{"type": "string", "format": "byte"}
	default:
		return nil
	}
}
