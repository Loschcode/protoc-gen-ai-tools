package generator

import (
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// presenceFile mirrors links_create: a plain proto3 string with no presence,
// and a google.protobuf.Timestamp, which has presence but carries no `optional`
// keyword because message fields never need one.
func presenceFile(t *testing.T) *protogen.File {
	t.Helper()

	fd := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/presence.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/timestamp.proto"},
		Options:    &descriptorpb.FileOptions{GoPackage: proto.String("example.com/gen/testv1;testv1")},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("CreateLinkRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("destination", 1, nil),
					msgField("expires_at", 2, ".google.protobuf.Timestamp", false),
					msgField("tags", 3, ".google.protobuf.Timestamp", true),
				},
			},
			{
				Name:  proto.String("CreateLinkResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{strField("id", 1, nil)},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("LinksService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("CreateLink"),
				InputType:  proto.String(".test.v1.CreateLinkRequest"),
				OutputType: proto.String(".test.v1.CreateLinkResponse"),
				Options:    rpcToolOptions("links_create", "Create a link", true),
			}},
		}},
	}

	timestamp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("google/protobuf/timestamp.proto"),
		Package: proto.String("google.protobuf"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("google.golang.org/protobuf/types/known/timestamppb")},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Timestamp"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("seconds"), Number: proto.Int32(1), Label: labelOptional.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()},
				{Name: proto.String("nanos"), Number: proto.Int32(2), Label: labelOptional.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum()},
			},
		}},
	}

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{fd.GetName()},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{timestamp, fd},
	}
	plugin, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New: %v", err)
	}
	return plugin.Files[len(plugin.Files)-1]
}

func presenceProps(t *testing.T) map[string]any {
	t.Helper()

	file := presenceFile(t)
	for _, m := range file.Messages {
		if m.Desc.Name() != "CreateLinkRequest" {
			continue
		}
		schema := NewSchemaGenerator(true).Generate(m.Desc)
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("request schema has no properties: %#v", schema)
		}
		return props
	}
	t.Fatal("CreateLinkRequest not found")
	return nil
}

// The failure this exists to prevent: strict mode puts every property in
// `required`, so a google.protobuf.Timestamp rendered as a bare date-time
// string left the model no way to say "no expiry". It had to emit some date,
// and it invented one on every link — an expiry that later deletes the link
// and its analytics. The field's own usage notes said never to invent one; the
// schema made obeying them impossible.
func TestSingularMessageFieldIsNullable(t *testing.T) {
	props := presenceProps(t)

	expires, ok := props["expires_at"].(map[string]any)
	if !ok {
		t.Fatalf("expires_at missing from properties: %#v", props)
	}
	branches, ok := expires["anyOf"].([]any)
	if !ok {
		t.Fatalf("a field with presence must accept null, got: %#v", expires)
	}
	if _, nulls := splitNullBranch(branches); nulls != 1 {
		t.Fatalf("expires_at has no null branch, so absence is inexpressible: %#v", expires)
	}
}

// A proto3 scalar has no presence: an unset string is the empty string, and
// making it nullable would offer the model two ways to say the same nothing.
func TestPlainScalarStaysNonNullable(t *testing.T) {
	props := presenceProps(t)

	destination, ok := props["destination"].(map[string]any)
	if !ok {
		t.Fatalf("destination missing from properties: %#v", props)
	}
	if _, isUnion := destination["anyOf"]; isUnion {
		t.Errorf("a presence-less scalar was made nullable: %#v", destination)
	}
	if destination["type"] != "string" {
		t.Errorf("destination is not a plain string: %#v", destination)
	}
}

// Repeated fields have no presence either — an absent list is an empty list.
func TestRepeatedFieldStaysNonNullable(t *testing.T) {
	props := presenceProps(t)

	tags, ok := props["tags"].(map[string]any)
	if !ok {
		t.Fatalf("tags missing from properties: %#v", props)
	}
	if _, isUnion := tags["anyOf"]; isUnion {
		t.Errorf("a repeated field was made nullable: %#v", tags)
	}
	if tags["type"] != "array" {
		t.Errorf("tags is not an array: %#v", tags)
	}
}

// Strict mode still needs every property named in `required` — nullability is
// how absence is expressed there, not omission.
func TestNullableFieldsStayInRequired(t *testing.T) {
	file := presenceFile(t)
	for _, m := range file.Messages {
		if m.Desc.Name() != "CreateLinkRequest" {
			continue
		}
		schema := NewSchemaGenerator(true).Generate(m.Desc)
		required, ok := schema["required"].([]string)
		if !ok {
			t.Fatalf("strict schema has no required list: %#v", schema)
		}
		found := false
		for _, name := range required {
			if name == "expires_at" {
				found = true
			}
		}
		if !found {
			t.Errorf("expires_at dropped out of required: %v", required)
		}
	}
}
