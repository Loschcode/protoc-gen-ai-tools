package generator

import (
	"sort"
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

	return objectAtRoot(sg.messageSchema(msg))
}

// objectAtRoot collapses a union into a single object.
//
// A message whose fields include a oneof is generated as an anyOf, which has
// no type of its own. That is fine inside a property and wrong at the root of a
// function's parameters: OpenAI requires an object there and rejects the whole
// tool list otherwise, with
//
//	schema must be a JSON Schema of 'type: "object"', got 'type: "None"'
//
// One malformed tool fails every request, so a single oneof on a request
// message took a production assistant down for two days, for every
// conversation, whatever it asked.
//
// The branches of a oneof differ only in which mutually exclusive member they
// carry, so their union describes the same message. A member required by only
// some branches cannot be required overall, which leaves exactly the fields
// every branch shares. Which members exclude each other belongs in the tool's
// description, where the model reads it, not in a schema shape the provider
// will not accept.
func objectAtRoot(schema map[string]any) map[string]any {
	if _, hasType := schema["type"]; hasType {
		return schema
	}
	branches, ok := schema["anyOf"].([]any)
	if !ok || len(branches) == 0 {
		return schema
	}

	properties := map[string]any{}
	requiredCount := map[string]int{}

	for _, raw := range branches {
		branch, ok := raw.(map[string]any)
		if !ok {
			return schema
		}
		if props, ok := branch["properties"].(map[string]any); ok {
			for name, definition := range props {
				properties[name] = definition
			}
		}
		if required, ok := branch["required"].([]string); ok {
			for _, name := range required {
				requiredCount[name]++
			}
		}
		if required, ok := branch["required"].([]any); ok {
			for _, name := range required {
				if field, ok := name.(string); ok {
					requiredCount[field]++
				}
			}
		}
	}

	required := make([]string, 0, len(requiredCount))
	for name, count := range requiredCount {
		if count == len(branches) {
			required = append(required, name)
		}
	}
	sort.Strings(required)

	flattened := map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
	if description, ok := schema["description"]; ok {
		flattened["description"] = description
	}
	for _, branch := range branches {
		if asMap, ok := branch.(map[string]any); ok {
			if additional, ok := asMap["additionalProperties"]; ok {
				flattened["additionalProperties"] = additional
				break
			}
		}
	}
	return flattened
}

// nullable makes a schema accept null, without nesting one union inside
// another.
//
// A nullable message that is itself a union would otherwise produce
// anyOf[ anyOf[caseA, caseB], null ]. The inner wrapper carries no type and
// OpenAI rejects the tool over it, reporting a required-key problem on a branch
// that has none, which is a hard error to read back to its cause.
// anyOf[anyOf[A,B],C] means anyOf[A,B,C], so the branches are spliced instead.
func nullable(schema map[string]any) map[string]any {
	null := map[string]any{"type": "null"}

	if branches, ok := schema["anyOf"].([]any); ok {
		if _, hasType := schema["type"]; !hasType {
			return map[string]any{"anyOf": append(append([]any{}, branches...), null)}
		}
	}
	return map[string]any{"anyOf": []any{schema, null}}
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

	// Members of a real oneof are collected rather than emitted inline: a oneof
	// is a union, and flattening it into sibling properties loses that. See
	// oneofSchema below for why that matters.
	type oneofMember struct {
		name   string
		schema map[string]any
	}
	var oneofMembers []oneofMember
	realOneofs := make(map[protoreflect.FullName]bool)

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

		desc := sg.fieldDescription(field)

		if desc != "" {
			schema["description"] = desc
		}

		// proto3 `optional` is implemented as a synthetic one-member oneof, so
		// only non-synthetic ones are real unions.
		if od := field.ContainingOneof(); od != nil && !od.IsSynthetic() {
			realOneofs[od.FullName()] = true
			oneofMembers = append(oneofMembers, oneofMember{name: name, schema: schema})
			continue
		}

		// In strict mode, optional fields are nullable via anyOf union.
		if sg.strict && isOptional {
			properties[name] = nullable(schema)
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

	// A single union is expressible as anyOf over one branch per member. More
	// than one in the same message would need the cartesian product of their
	// members, which is a combinatorial blow-up for no real-world gain, so
	// those keep the flattened form.
	if len(realOneofs) == 1 && len(oneofMembers) > 0 {
		branches := make([]any, 0, len(oneofMembers))
		for _, m := range oneofMembers {
			props := make(map[string]any, len(properties)+1)
			for k, v := range properties {
				props[k] = v
			}
			props[m.name] = m.schema

			branch := map[string]any{
				"type":       "object",
				"properties": props,
				"required":   append(append([]string{}, required...), m.name),
			}
			if sg.strict {
				branch["additionalProperties"] = false
			}
			branches = append(branches, branch)
		}
		return map[string]any{"anyOf": branches}
	}

	for _, m := range oneofMembers {
		if sg.strict {
			wrapper := nullable(m.schema)
			if desc, ok := m.schema["description"]; ok {
				wrapper["description"] = desc
				delete(m.schema, "description")
			}
			properties[m.name] = wrapper
			required = append(required, m.name)
		} else {
			properties[m.name] = m.schema
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
		result := map[string]any{"type": "object"}
		if sg.strict {
			result["additionalProperties"] = false
		}
		return result
	case "google.protobuf.Struct":
		if sg.strict {
			// Struct has dynamic keys — incompatible with strict mode.
			// Represent as JSON string.
			return map[string]any{"type": "string", "description": "JSON-encoded object"}
		}
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

// fieldDescription composes the description the model sees for a field.
//
// A leading comment serves developers, the OpenAPI spec and the model at once,
// so behavioural coaching written there leaks into a public API reference. The
// tool_field annotation gives the model its own channel:
//
//	description  replaces the comment outright (use sparingly)
//	usage_notes  is appended to it (the common case)
func (sg *SchemaGenerator) fieldDescription(field protoreflect.FieldDescriptor) string {
	opts := annotations.GetToolFieldOpts(field)

	desc := opts.Description
	if desc == "" {
		desc = annotations.FieldDescription(field)
	}
	if desc == "" {
		si := field.ParentFile().SourceLocations().ByDescriptor(field)
		desc = strings.TrimSpace(si.LeadingComments)
	}

	return composeDescription(desc, opts.UsageNotes, "")
}

// composeDescription joins a factual description with LLM-only usage notes.
// override, when non-empty, replaces the description before notes are applied.
func composeDescription(desc, usageNotes, override string) string {
	if override != "" {
		desc = override
	}
	desc = strings.TrimSpace(desc)
	notes := strings.TrimSpace(usageNotes)
	switch {
	case notes == "":
		return desc
	case desc == "":
		return notes
	default:
		return desc + "\n\n" + notes
	}
}
