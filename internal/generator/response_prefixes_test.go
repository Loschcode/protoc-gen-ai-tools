package generator

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// --- helpers to build annotated descriptors ------------------------------

const (
	extToolField  protowire.Number = 52105
	extRPCTool    protowire.Number = 52104
	fieldTypeStr                   = descriptorpb.FieldDescriptorProto_TYPE_STRING
	fieldTypeMsg                   = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	labelOptional                  = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	labelRepeated                  = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
)

// toolFieldOptions builds FieldOptions carrying (ai.tools.v1.tool_field).
func toolFieldOptions(aliasPrefix string, skip bool) *descriptorpb.FieldOptions {
	var inner []byte
	if skip {
		inner = protowire.AppendTag(inner, 1, protowire.VarintType)
		inner = protowire.AppendVarint(inner, 1)
	}
	if aliasPrefix != "" {
		inner = protowire.AppendTag(inner, 2, protowire.BytesType)
		inner = protowire.AppendString(inner, aliasPrefix)
	}
	var raw []byte
	raw = protowire.AppendTag(raw, extToolField, protowire.BytesType)
	raw = protowire.AppendBytes(raw, inner)

	opts := &descriptorpb.FieldOptions{}
	opts.ProtoReflect().SetUnknown(protoreflect.RawFields(raw))
	return opts
}

// rpcToolOptions builds MethodOptions carrying (ai.tools.v1.rpc_tool).
func rpcToolOptions(name, description string, autoExecute bool) *descriptorpb.MethodOptions {
	var inner []byte
	inner = protowire.AppendTag(inner, 1, protowire.BytesType)
	inner = protowire.AppendString(inner, name)
	inner = protowire.AppendTag(inner, 2, protowire.BytesType)
	inner = protowire.AppendString(inner, description)
	if autoExecute {
		inner = protowire.AppendTag(inner, 4, protowire.VarintType)
		inner = protowire.AppendVarint(inner, 1)
	}
	var raw []byte
	raw = protowire.AppendTag(raw, extRPCTool, protowire.BytesType)
	raw = protowire.AppendBytes(raw, inner)

	opts := &descriptorpb.MethodOptions{}
	opts.ProtoReflect().SetUnknown(protoreflect.RawFields(raw))
	return opts
}

func strField(name string, num int32, opts *descriptorpb.FieldOptions) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:    proto.String(name),
		Number:  proto.Int32(num),
		Label:   labelOptional.Enum(),
		Type:    fieldTypeStr.Enum(),
		Options: opts,
	}
}

func msgField(name string, num int32, typeName string, repeated bool) *descriptorpb.FieldDescriptorProto {
	label := labelOptional
	if repeated {
		label = labelRepeated
	}
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(num),
		Label:    label.Enum(),
		Type:     fieldTypeMsg.Enum(),
		TypeName: proto.String(typeName),
	}
}

// testFile mirrors the real-world shape that exposed the bug:
//   - CreateLinkRequest.id is skipped (no request-side alias for the entity)
//   - CreateLinkResponse.link carries id/workspaceId/pageThemeId
//   - ListWorkflowStepsRequest only has link_id, but the response holds step ids
func testFile(t *testing.T, autoExecute bool) *protogen.File {
	t.Helper()

	fd := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/test.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/gen/testv1;testv1")},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Link"),
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("id", 1, toolFieldOptions("link", false)),
					strField("workspace_id", 2, nil),
					strField("page_theme_id", 3, toolFieldOptions("theme", false)),
					strField("qrcode_design_id", 4, nil),
				},
			},
			{
				Name: proto.String("WorkflowStep"),
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("id", 1, toolFieldOptions("step", false)),
					strField("link_id", 2, toolFieldOptions("link", false)),
				},
			},
			{
				Name: proto.String("CreateLinkRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("destination", 1, nil),
					strField("id", 2, toolFieldOptions("", true)),
				},
			},
			{
				Name:  proto.String("CreateLinkResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{msgField("link", 1, ".test.v1.Link", false)},
			},
			{
				Name: proto.String("ListWorkflowStepsRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					strField("link_id", 1, toolFieldOptions("link", false)),
				},
			},
			{
				Name:  proto.String("ListWorkflowStepsResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{msgField("workflow_steps", 1, ".test.v1.WorkflowStep", true)},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("LinkService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("CreateLink"),
						InputType:  proto.String(".test.v1.CreateLinkRequest"),
						OutputType: proto.String(".test.v1.CreateLinkResponse"),
						Options:    rpcToolOptions("links_create", "Create a link", autoExecute),
					},
					{
						Name:       proto.String("ListWorkflowSteps"),
						InputType:  proto.String(".test.v1.ListWorkflowStepsRequest"),
						OutputType: proto.String(".test.v1.ListWorkflowStepsResponse"),
						Options:    rpcToolOptions("workflow_steps_list", "List workflow steps", autoExecute),
					},
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

func collectTestTools(t *testing.T) map[string]ToolInfo {
	t.Helper()
	c := NewCollector()
	c.CollectFile(testFile(t, true))
	byName := make(map[string]ToolInfo, len(c.tools))
	for _, tool := range c.tools {
		byName[tool.Name] = tool
	}
	return byName
}

// --- tests ---------------------------------------------------------------

func TestResponseFieldPrefixesUseJSONNames(t *testing.T) {
	tools := collectTestTools(t)

	create, ok := tools["links_create"]
	if !ok {
		t.Fatal("links_create not collected")
	}
	want := map[string]string{
		"id":          "", // ambiguous — defers to the tool's primary prefix
		"pageThemeId": "theme",
	}
	if len(create.ResponseFieldPrefixes) != len(want) {
		t.Fatalf("links_create response prefixes = %v, want %v", create.ResponseFieldPrefixes, want)
	}
	for field, prefix := range want {
		got, present := create.ResponseFieldPrefixes[field]
		if !present || got != prefix {
			t.Errorf("links_create response prefix for %q = %q (present=%v), want %q", field, got, present, prefix)
		}
	}
	// Unannotated fields must be absent so they never get aliased.
	for _, field := range []string{"workspaceId", "qrcodeDesignId", "workspace_id"} {
		if _, present := create.ResponseFieldPrefixes[field]; present {
			t.Errorf("unannotated field %q must not be in the response prefix map", field)
		}
	}
}

func TestOutputPrefixComesFromResponse(t *testing.T) {
	tools := collectTestTools(t)

	// Regression: CreateLinkRequest.id is skipped, so the request yields no
	// prefix at all. The response's link.id must still give "link".
	if got := tools["links_create"].OutputPrefix; got != "link" {
		t.Errorf("links_create OutputPrefix = %q, want %q", got, "link")
	}

	// Regression: ListWorkflowStepsRequest only has link_id, but the response
	// carries STEP ids — they must not be registered as "link".
	if got := tools["workflow_steps_list"].OutputPrefix; got != "step" {
		t.Errorf("workflow_steps_list OutputPrefix = %q, want %q", got, "step")
	}
}

func TestListResponseNestedPrefixes(t *testing.T) {
	tools := collectTestTools(t)

	list := tools["workflow_steps_list"]
	if got := list.ResponseFieldPrefixes["linkId"]; got != "link" {
		t.Errorf("linkId prefix = %q, want %q", got, "link")
	}
	if got, ok := list.ResponseFieldPrefixes["id"]; !ok || got != "" {
		t.Errorf("id prefix = %q (present=%v), want \"\"", got, ok)
	}
}

func TestGeneratedCodeIsFieldAware(t *testing.T) {
	c := NewCollector()
	c.CollectFile(testFile(t, true))
	out := c.Generate()

	mustContain := []string{
		"var responseFieldPrefixes = map[string]string{",
		`"id":          "", // use the tool's primary prefix`,
		`"linkId":      "link",`,
		`"pageThemeId": "theme",`,
		"func (am *AliasManager) RegisterFieldsFromJSON(primaryPrefix string, jsonStr string) {",
		"func (am *AliasManager) registerValue(primaryPrefix string, fieldName string, value any) {",
		// RegisterNewUUIDs stays exported for external callers...
		"func (am *AliasManager) RegisterNewUUIDs(prefix string, jsonStr string) {",
		`"links_create":        "link",`,
		`"workflow_steps_list": "step",`,
		`e.aliases.RegisterFieldsFromJSON(toolOutputPrefix["links_create"], string(out))`,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("generated code missing %q", s)
		}
	}

	// ...but executors must no longer call it.
	if strings.Contains(out, "e.aliases.RegisterNewUUIDs(") {
		t.Error("generated executors must not call RegisterNewUUIDs")
	}
	// Unannotated response fields must never appear in the prefix map.
	if strings.Contains(out, `"workspaceId"`) || strings.Contains(out, `"qrcodeDesignId"`) {
		t.Error("unannotated response fields leaked into responseFieldPrefixes")
	}
}

func TestGeneratedCodeIsGofmtClean(t *testing.T) {
	c := NewCollector()
	c.CollectFile(testFile(t, true))
	src := c.Generate() + "\n"

	formatted, err := format.Source([]byte(src))
	if err != nil {
		t.Fatalf("generated code does not parse: %v", err)
	}
	if string(formatted) != src {
		t.Errorf("generated code is not gofmt-clean:\n--- got ---\n%s\n--- want ---\n%s", src, formatted)
	}
}

// TestGeneratedAliasRuntime compiles the generated AliasManager into a throwaway
// module and exercises RegisterFieldsFromJSON against realistic responses.
// auto_execute is off so the generated file has no gRPC/proto dependencies.
func TestGeneratedAliasRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile-and-run test in short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	c := NewCollector()
	c.CollectFile(testFile(t, false))

	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module gentoolsruntime\n\ngo 1.25\n")
	write("ai_tools.gen.go", c.Generate()+"\n")
	write("runtime_test.go", generatedRuntimeTestSource)

	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated runtime test failed: %v\n%s", err, out)
	}
}

const generatedRuntimeTestSource = `package gentools

import "testing"

const (
	linkUUID      = "11111111-1111-4111-8111-111111111111"
	workspaceUUID = "22222222-2222-4222-8222-222222222222"
	themeUUID     = "33333333-3333-4333-8333-333333333333"
	stepUUID      = "44444444-4444-4444-8444-444444444444"
)

// A create response: only the entity id and annotated reference fields get
// aliased. workspaceId and qrcodeDesignId must be left alone.
func TestRegisterFieldsFromJSONCreate(t *testing.T) {
	am := NewAliasManager()
	resp := ` + "`" + `{"link":{"id":"` + "` + linkUUID + `" + `","workspaceId":"` + "` + workspaceUUID + `" + `","pageThemeId":"` + "` + themeUUID + `" + `","qrcodeDesignId":"55555555-5555-4555-8555-555555555555"}}` + "`" + `

	am.RegisterFieldsFromJSON("link", resp)

	if got := am.ResolveAlias("link-1"); got != linkUUID {
		t.Errorf("link-1 = %q, want the link id", got)
	}
	if got := am.ResolveAlias("theme-1"); got != themeUUID {
		t.Errorf("theme-1 = %q, want the page theme id", got)
	}
	if got := am.ResolveAlias("link-2"); got != "link-2" {
		t.Errorf("unrelated UUID was aliased as link-2 -> %q", got)
	}
	out := am.AliasifyJSON("links_create", resp)
	if !contains(out, workspaceUUID) {
		t.Error("workspaceId must stay a raw UUID")
	}
}

// A list response: ids inside a repeated message use the tool's primary prefix
// for that response ("step"), never the request's "link".
func TestRegisterFieldsFromJSONList(t *testing.T) {
	am := NewAliasManager()
	resp := ` + "`" + `{"workflowSteps":[{"id":"` + "` + stepUUID + `" + `","linkId":"` + "` + linkUUID + `" + `"}]}` + "`" + `

	am.RegisterFieldsFromJSON("step", resp)

	if got := am.ResolveAlias("step-1"); got != stepUUID {
		t.Errorf("step-1 = %q, want the step id", got)
	}
	if got := am.ResolveAlias("link-1"); got != linkUUID {
		t.Errorf("link-1 = %q, want the link id", got)
	}
}

func TestRegisterFieldsFromJSONIgnoresUnknownFields(t *testing.T) {
	am := NewAliasManager()
	am.RegisterFieldsFromJSON("link", ` + "`" + `{"somethingElse":"` + "` + workspaceUUID + `" + `"}` + "`" + `)
	if got := am.ResolveAlias("link-1"); got != "link-1" {
		t.Errorf("unknown field was aliased: link-1 -> %q", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
`
