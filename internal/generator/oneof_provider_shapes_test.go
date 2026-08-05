package generator

import (
	"fmt"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// Generating a oneof as a union saved real tokens, and produced two shapes
// OpenAI refuses. It validates the whole tool list before running anything, so
// one bad schema fails every request: a production assistant answered nothing
// at all for two days, whatever it was asked, and the second shape was only
// discovered after the first was fixed, because it reports one tool at a time.
//
//	schema must be a JSON Schema of 'type: "object"', got 'type: "None"'
//	In context=('properties','badge','anyOf','0','anyOf','1'), 'required' is
//	required to be supplied ... Extra required key 'delete' supplied.
//
// Both are asserted here against the shapes the generator emits, rather than
// against the tools that happened to break.

// A request message carrying a oneof is still an object at the root, because
// function parameters have to be one.
func TestGenerate_RequestWithAOneofIsAnObjectAtItsRoot(t *testing.T) {
	schema := requestSchemaWithRootOneof(t)

	kind, ok := schema["type"].(string)
	if !ok {
		t.Fatalf("no type at the root: %v", schema)
	}
	if kind != "object" {
		t.Fatalf("root type is %q, want object", kind)
	}
	if _, stillUnion := schema["anyOf"]; stillUnion {
		t.Error("the root is still an anyOf")
	}

	properties, _ := schema["properties"].(map[string]any)
	// Both members of the oneof survive: the model must be able to send either.
	for _, member := range []string{"image_data", "image_delete"} {
		if _, present := properties[member]; !present {
			t.Errorf("%s was dropped, so that case can no longer be expressed", member)
		}
	}
	// The shared field stays required; neither exclusive member can be.
	required := toStringSet(schema["required"])
	if !required["id"] {
		t.Error("id is required by every branch and should stay required")
	}
	for _, member := range []string{"image_data", "image_delete"} {
		if required[member] {
			t.Errorf("%s is required, which makes the other case impossible to send", member)
		}
	}
}

// A nullable message that is itself a union yields one flat anyOf, not a union
// nested inside a union.
func TestNullable_DoesNotNestAUnionInsideAUnion(t *testing.T) {
	union := map[string]any{"anyOf": []any{
		map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}},
		map[string]any{"type": "object", "properties": map[string]any{"delete": map[string]any{"type": "object"}}},
	}}

	branches, ok := nullable(union)["anyOf"].([]any)
	if !ok {
		t.Fatal("nullable did not produce an anyOf")
	}
	if len(branches) != 3 {
		t.Fatalf("got %d branches, want the two cases plus null", len(branches))
	}
	for i, branch := range branches {
		asMap, ok := branch.(map[string]any)
		if !ok {
			continue
		}
		if _, nested := asMap["anyOf"]; nested {
			t.Errorf("branch %d is still a union", i)
		}
	}
}

// An ordinary schema is wrapped the usual way.
func TestNullable_WrapsAPlainSchema(t *testing.T) {
	branches, ok := nullable(map[string]any{"type": "string"})["anyOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("got %v, want the schema plus null", branches)
	}
}

func toStringSet(raw any) map[string]bool {
	set := map[string]bool{}
	switch typed := raw.(type) {
	case []string:
		for _, name := range typed {
			set[name] = true
		}
	case []any:
		for _, name := range typed {
			if field, ok := name.(string); ok {
				set[field] = true
			}
		}
	}
	return set
}

// requestSchemaWithRootOneof builds the shape that broke first: a request
// message whose own fields include a two-member oneof, as
// UpdateQrcodeDesignRequest has for central_image_data / central_image_delete.
func requestSchemaWithRootOneof(t *testing.T) map[string]any {
	t.Helper()

	fd := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/rootoneof.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/gen/testv1;testv1")},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:      proto.String("UpdateDesignRequest"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("central_image")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("id", 1, nil),
					func() *descriptorpb.FieldDescriptorProto {
						f := strField("image_data", 2, nil)
						f.OneofIndex = proto.Int32(0)
						return f
					}(),
					func() *descriptorpb.FieldDescriptorProto {
						f := strField("image_delete", 3, nil)
						f.OneofIndex = proto.Int32(0)
						return f
					}(),
				},
			},
			{Name: proto.String("UpdateDesignResponse"), Field: []*descriptorpb.FieldDescriptorProto{strField("id", 1, nil)}},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("DesignService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("UpdateDesign"),
				InputType:  proto.String(".test.v1.UpdateDesignRequest"),
				OutputType: proto.String(".test.v1.UpdateDesignResponse"),
				Options:    rpcToolOptions("qrcode_design_update", "Update a design", true),
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

	file := plugin.Files[0]
	var request *protogen.Message
	for _, m := range file.Messages {
		if m.Desc.Name() == "UpdateDesignRequest" {
			request = m
		}
	}
	if request == nil {
		t.Fatal("request message not found")
	}

	return NewSchemaGenerator(true).Generate(request.Desc)
}

// In strict mode every object declares properties and required, empty ones
// included.
//
// google.protobuf.Empty is how a valueless case in a oneof is written —
// MediaSelection uses it for "delete this badge" — and it was generated as
// {"type":"object"} with neither key. Strict mode reads a missing required as
// "not supplied" rather than "nothing required" and rejects the whole tool:
//
//	'required' is required to be supplied and to be an array including every
//	key in properties. Extra required key 'delete' supplied.
//
// The message names the parent branch, which is valid, so it takes a while to
// find the object it is actually complaining about. Asserting the invariant
// over every object says it plainly.
func TestGenerate_EveryStrictObjectDeclaresPropertiesAndRequired(t *testing.T) {
	schema := requestSchemaWithEmptyOneofCase(t)

	for _, problem := range objectsMissingStrictKeys(schema, "") {
		t.Error(problem)
	}
}

func objectsMissingStrictKeys(node any, path string) []string {
	var problems []string

	switch typed := node.(type) {
	case map[string]any:
		if kind, _ := typed["type"].(string); kind == "object" {
			where := path
			if where == "" {
				where = "root"
			}
			if _, ok := typed["properties"]; !ok {
				problems = append(problems, where+": object without properties")
			}
			if _, ok := typed["required"]; !ok {
				problems = append(problems, where+": object without required")
			}
		}
		for name, child := range typed {
			problems = append(problems, objectsMissingStrictKeys(child, path+"."+name)...)
		}
	case []any:
		for i, child := range typed {
			problems = append(problems, objectsMissingStrictKeys(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}

	return problems
}

// requestSchemaWithEmptyOneofCase mirrors MediaSelection: a oneof whose second
// case is google.protobuf.Empty, nested under a field of the request.
func requestSchemaWithEmptyOneofCase(t *testing.T) map[string]any {
	t.Helper()

	fd := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/emptycase.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/empty.proto"},
		Options:    &descriptorpb.FileOptions{GoPackage: proto.String("example.com/gen/testv1;testv1")},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:      proto.String("MediaSelection"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("value")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					func() *descriptorpb.FieldDescriptorProto {
						f := strField("id", 1, nil)
						f.OneofIndex = proto.Int32(0)
						return f
					}(),
					oneofMsgField("delete", 2, ".google.protobuf.Empty", 0),
				},
			},
			{
				Name:  proto.String("UpdateThemeRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{msgField("badge", 1, ".test.v1.MediaSelection", false)},
			},
			{Name: proto.String("UpdateThemeResponse"), Field: []*descriptorpb.FieldDescriptorProto{strField("id", 1, nil)}},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("ThemeService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("UpdateTheme"),
				InputType:  proto.String(".test.v1.UpdateThemeRequest"),
				OutputType: proto.String(".test.v1.UpdateThemeResponse"),
				Options:    rpcToolOptions("page_themes_update", "Update a theme", true),
			}},
		}},
	}

	emptyFd := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("google/protobuf/empty.proto"),
		Package:     proto.String("google.protobuf"),
		Syntax:      proto.String("proto3"),
		Options:     &descriptorpb.FileOptions{GoPackage: proto.String("google.golang.org/protobuf/types/known/emptypb")},
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Empty")}},
	}

	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{fd.GetName()},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{emptyFd, fd},
	}
	plugin, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New: %v", err)
	}

	file := plugin.Files[len(plugin.Files)-1]
	var request *protogen.Message
	for _, m := range file.Messages {
		if m.Desc.Name() == "UpdateThemeRequest" {
			request = m
		}
	}
	if request == nil {
		t.Fatal("request message not found")
	}

	return NewSchemaGenerator(true).Generate(request.Desc)
}
