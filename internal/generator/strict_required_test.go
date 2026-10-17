package generator

import (
	"fmt"
	"sort"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// strictFile mirrors the tools OpenAI rejected or forced into a value:
// ImportMediaRequest with a scalar oneof at its root, and ListMediaRequest with
// enum filters whose unset value means "any".
func strictFile(t *testing.T) *protogen.File {
	t.Helper()

	enumField := func(name string, num int32, typeName string) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{
			Name:     proto.String(name),
			Number:   proto.Int32(num),
			Label:    labelOptional.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
			TypeName: proto.String(typeName),
		}
	}
	oneofStr := func(name string, num, index int32) *descriptorpb.FieldDescriptorProto {
		f := strField(name, num, nil)
		f.OneofIndex = proto.Int32(index)
		return f
	}
	optionalEnum := enumField("sort", 4, ".test.v1.Sort")
	optionalEnum.OneofIndex = proto.Int32(0)
	optionalEnum.Proto3Optional = proto.Bool(true)
	requiredKind := enumField("required_kind", 6, ".test.v1.Kind")
	var behavior []byte
	behavior = protowire.AppendTag(behavior, 1052, protowire.VarintType)
	behavior = protowire.AppendVarint(behavior, 2)
	requiredKind.Options = &descriptorpb.FieldOptions{}
	requiredKind.Options.ProtoReflect().SetUnknown(protoreflect.RawFields(behavior))
	repeatedKinds := enumField("kinds", 5, ".test.v1.Kind")
	repeatedKinds.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()

	value := func(name string, num int32) *descriptorpb.EnumValueDescriptorProto {
		return &descriptorpb.EnumValueDescriptorProto{Name: proto.String(name), Number: proto.Int32(num)}
	}

	fd := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/strict.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/gen/testv1;testv1")},
		EnumType: []*descriptorpb.EnumDescriptorProto{
			{Name: proto.String("Kind"), Value: []*descriptorpb.EnumValueDescriptorProto{value("KIND_UNSPECIFIED", 0), value("KIND_IMAGE", 1), value("KIND_PDF", 2)}},
			{Name: proto.String("Sort"), Value: []*descriptorpb.EnumValueDescriptorProto{value("SORT_NEWEST", 0), value("SORT_OLDEST", 1)}},
			{Name: proto.String("Direction"), Value: []*descriptorpb.EnumValueDescriptorProto{value("DIRECTION_ASC", 0), value("DIRECTION_DESC", 1)}},
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:      proto.String("ImportMediaRequest"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("source")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					oneofStr("url", 1, 0),
					oneofStr("assistant_attachment_id", 2, 0),
					strField("file_name", 3, nil),
					enumField("uploaded_from", 4, ".test.v1.Kind"),
				},
			},
			{
				Name:      proto.String("ListMediaRequest"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_sort")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("page_token", 1, nil),
					enumField("kind", 2, ".test.v1.Kind"),
					enumField("direction", 3, ".test.v1.Direction"),
					optionalEnum,
					repeatedKinds,
					requiredKind,
				},
			},
		},
	}

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{fd.GetName()},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fd},
	}
	plugin, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New: %v", err)
	}
	return plugin.Files[0]
}

func strictSchema(t *testing.T, message string) map[string]any {
	t.Helper()
	for _, m := range strictFile(t).Messages {
		if string(m.Desc.Name()) == message {
			return NewSchemaGenerator(true).Generate(m.Desc)
		}
	}
	t.Fatalf("%s not found", message)
	return nil
}

// OpenAI rejected the whole tool list, every request, with
//
//	Invalid schema for function 'media_import': ... Missing 'assistant_attachment_id'
//
// because a scalar oneof member at the root was a property but not required.
func TestStrict_RootScalarOneofMembersAreRequiredAndNullable(t *testing.T) {
	schema := strictSchema(t, "ImportMediaRequest")
	properties := schema["properties"].(map[string]any)
	required := toStringSet(schema["required"])

	for _, member := range []string{"url", "assistant_attachment_id"} {
		if !required[member] {
			t.Errorf("%s missing from required", member)
		}
		if !isNullable(properties[member]) {
			t.Errorf("%s does not accept null: %v", member, properties[member])
		}
	}
	if isNullable(properties["file_name"]) {
		t.Error("a plain scalar without presence became nullable")
	}
	for _, problem := range strictViolations(schema, "root") {
		t.Error(problem)
	}
}

func TestStrict_EnumFiltersAcceptNull(t *testing.T) {
	schema := strictSchema(t, "ListMediaRequest")
	properties := schema["properties"].(map[string]any)
	required := toStringSet(schema["required"])

	cases := map[string]bool{
		"kind":      true,  // zero value is KIND_UNSPECIFIED
		"sort":      true,  // proto3 optional
		"direction": false, // zero value is a real choice
		"kinds":     false, // repeated: an empty list is absence
		// field_behavior REQUIRED: there is no "any" to send.
		"required_kind": false,
	}
	for name, wantNullable := range cases {
		if !required[name] {
			t.Errorf("%s missing from required", name)
		}
		if got := isNullable(properties[name]); got != wantNullable {
			t.Errorf("%s nullable = %v, want %v: %v", name, got, wantNullable, properties[name])
		}
	}

	for _, problem := range strictViolations(schema, "root") {
		t.Error(problem)
	}
}

// A non-strict tool has no null to send, so an enum whose zero means unset is
// simply not required.
func TestNonStrict_UnspecifiedEnumIsNotRequired(t *testing.T) {
	for _, m := range strictFile(t).Messages {
		if m.Desc.Name() != "ListMediaRequest" {
			continue
		}
		schema := NewSchemaGenerator(false).Generate(m.Desc)
		required := toStringSet(schema["required"])
		if required["kind"] {
			t.Error("kind is required, so the model cannot list every kind")
		}
		if !required["direction"] {
			t.Error("direction has no unset value and should stay required")
		}
		if !required["required_kind"] {
			t.Error("required_kind is field_behavior REQUIRED and should stay required")
		}
	}
}

// strictViolations checks the rules OpenAI enforces on a strict function
// schema: every object closes additionalProperties and lists every property in
// required.
func strictViolations(node any, path string) []string {
	var problems []string
	switch typed := node.(type) {
	case map[string]any:
		if kind, _ := typed["type"].(string); kind == "object" {
			if typed["additionalProperties"] != false {
				problems = append(problems, path+": additionalProperties is not false")
			}
			properties, _ := typed["properties"].(map[string]any)
			keys := make([]string, 0, len(properties))
			for k := range properties {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			required := toStringSet(typed["required"])
			if len(required) != len(keys) {
				problems = append(problems, fmt.Sprintf("%s: required %v does not match properties %v", path, required, keys))
			}
			for _, k := range keys {
				if !required[k] {
					problems = append(problems, fmt.Sprintf("%s: %s missing from required", path, k))
				}
			}
		}
		for key, child := range typed {
			problems = append(problems, strictViolations(child, path+"."+key)...)
		}
	case []any:
		for i, child := range typed {
			problems = append(problems, strictViolations(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return problems
}
