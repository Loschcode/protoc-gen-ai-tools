package generator

import (
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// oneofFile builds a request message shaped like the real workflow step create:
// a scalar sibling, a proto3 `optional` scalar (a synthetic oneof), and a
// message field whose type holds a genuine multi-member oneof.
func oneofFile(t *testing.T) *protogen.File {
	t.Helper()

	fd := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/oneof.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/gen/testv1;testv1")},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:  proto.String("FormPayload"),
				Field: []*descriptorpb.FieldDescriptorProto{strField("title", 1, nil)},
			},
			{
				Name:  proto.String("RedirectPayload"),
				Field: []*descriptorpb.FieldDescriptorProto{strField("destination", 1, nil)},
			},
			{
				Name:      proto.String("StepPayload"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("payload")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					oneofMsgField("form", 1, ".test.v1.FormPayload", 0),
					oneofMsgField("redirect", 2, ".test.v1.RedirectPayload", 0),
				},
			},
			{
				Name: proto.String("CreateStepRequest"),
				// The synthetic oneof backing proto3 `optional note`.
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_note")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("link_id", 1, nil),
					msgField("payload", 2, ".test.v1.StepPayload", false),
					{
						Name:           proto.String("note"),
						Number:         proto.Int32(3),
						Label:          labelOptional.Enum(),
						Type:           fieldTypeStr.Enum(),
						OneofIndex:     proto.Int32(0),
						Proto3Optional: proto.Bool(true),
					},
				},
			},
			{
				Name:  proto.String("CreateStepResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{strField("id", 1, nil)},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("StepService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("CreateStep"),
				InputType:  proto.String(".test.v1.CreateStepRequest"),
				OutputType: proto.String(".test.v1.CreateStepResponse"),
				Options:    rpcToolOptions("workflow_steps_create", "Create a step", true),
			}},
		}},
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

func oneofMsgField(name string, num int32, typeName string, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	f := msgField(name, num, typeName, false)
	f.OneofIndex = proto.Int32(oneofIndex)
	return f
}

func stepPayloadSchema(t *testing.T) map[string]any {
	t.Helper()

	file := oneofFile(t)
	var msg *protogen.Message
	for _, m := range file.Messages {
		if m.Desc.Name() == "CreateStepRequest" {
			msg = m
		}
	}
	if msg == nil {
		t.Fatal("CreateStepRequest not found")
	}

	schema := NewSchemaGenerator(true).Generate(msg.Desc)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("request schema has no properties: %#v", schema)
	}
	payload, ok := props["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload is not an object schema: %#v", props["payload"])
	}
	return payload
}

// A oneof is a union. Flattening it into sibling nullable properties forces the
// model to emit every branch it did not pick as an explicit null, which is the
// cost this exists to prevent.
func TestOneofBecomesAnyOfOfBranches(t *testing.T) {
	payload := stepPayloadSchema(t)

	branches, ok := payload["anyOf"].([]any)
	if !ok {
		t.Fatalf("oneof did not produce anyOf, got: %#v", payload)
	}
	if len(branches) != 2 {
		t.Fatalf("expected one branch per oneof member, got %d", len(branches))
	}
	if _, flattened := payload["properties"]; flattened {
		t.Error("oneof still emitted flattened sibling properties")
	}

	seen := make(map[string]bool)
	for _, b := range branches {
		branch := b.(map[string]any)

		required, _ := branch["required"].([]string)
		if len(required) != 1 {
			t.Fatalf("branch should require exactly its own member, got %v", required)
		}
		seen[required[0]] = true

		props := branch["properties"].(map[string]any)
		if len(props) != 1 {
			t.Errorf("branch %q carries sibling members: %v", required[0], props)
		}
		if _, present := props[required[0]]; !present {
			t.Errorf("branch %q does not define its own member", required[0])
		}
		if branch["additionalProperties"] != false {
			t.Errorf("branch %q is not closed for strict mode", required[0])
		}
	}

	for _, member := range []string{"form", "redirect"} {
		if !seen[member] {
			t.Errorf("no branch for oneof member %q", member)
		}
	}
}

// proto3 `optional` compiles to a synthetic single-member oneof. Treating those
// as unions would turn every optional scalar into its own anyOf.
func TestSyntheticOneofStaysANullableProperty(t *testing.T) {
	file := oneofFile(t)
	var msg *protogen.Message
	for _, m := range file.Messages {
		if m.Desc.Name() == "CreateStepRequest" {
			msg = m
		}
	}

	schema := NewSchemaGenerator(true).Generate(msg.Desc)
	if _, isUnion := schema["anyOf"]; isUnion {
		t.Fatal("synthetic oneof turned the whole request into a union")
	}

	props := schema["properties"].(map[string]any)
	note, ok := props["note"].(map[string]any)
	if !ok {
		t.Fatalf("note missing from properties: %#v", props)
	}
	variants, ok := note["anyOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("optional scalar should be a nullable anyOf pair, got: %#v", note)
	}

	required, _ := schema["required"].([]string)
	var hasNote bool
	for _, r := range required {
		if r == "note" {
			hasNote = true
		}
	}
	if !hasNote {
		t.Error("strict mode requires every property to be listed in required")
	}
}
