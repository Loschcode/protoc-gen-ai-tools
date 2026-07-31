package generator

import (
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/Loschcode/protoc-gen-ai-tools/internal/annotations"
)

// AliasField describes a field that should be aliased for the LLM.
type AliasField struct {
	FieldName   string // JSON field name (e.g. "link_id", "id")
	AliasPrefix string // prefix for aliases (e.g. "link", "step")
}

// ToolInfo holds the data for a single AI agent tool.
type ToolInfo struct {
	VarName        string
	Name           string
	Description    string
	Strict         bool
	Schema         json.RawMessage
	AutoExecute    bool
	ServiceName    string
	MethodName     string
	InputTypeName  string
	OutputTypeName string
	GoImportPath   string
	AliasFields    []AliasField // fields that need alias resolution
	// OutputPrefix is the primary prefix for the entity returned by this RPC.
	// It is derived from the id field of the top-level entity in the RESPONSE
	// message, falling back to the request-derived prefix.
	OutputPrefix string
	// ResponseFieldPrefixes maps JSON field names found anywhere in the
	// response message to the alias prefix declared on that field.
	// A key of "id" always maps to "" meaning "use the tool's primary prefix".
	ResponseFieldPrefixes map[string]string
}

// Collector gathers annotated RPCs across proto files.
type Collector struct {
	tools []ToolInfo
}

// NewCollector creates a new tool collector.
func NewCollector() *Collector {
	return &Collector{}
}

// CollectFile processes a single proto file for rpc_tool annotations.
func (c *Collector) CollectFile(file *protogen.File) {
	for _, svc := range file.Services {
		for _, method := range svc.Methods {
			td, ok := annotations.ToolFromMethod(method.Desc)
			if !ok {
				continue
			}

			sg := NewSchemaGenerator(td.Strict)
			schemaMap := sg.Generate(method.Input.Desc)

			schemaJSON, err := json.Marshal(schemaMap)
			if err != nil {
				continue
			}

			// Collect alias fields from the request message
			var aliasFields []AliasField
			var outputPrefix string
			fields := method.Input.Desc.Fields()
			for i := 0; i < fields.Len(); i++ {
				f := fields.Get(i)
				opts := annotations.GetToolFieldOpts(f)
				if opts.AliasPrefix != "" {
					aliasFields = append(aliasFields, AliasField{
						FieldName:   string(f.Name()),
						AliasPrefix: opts.AliasPrefix,
					})
					// The "id" field (not "link_id", "step_id") is the entity
					// being created/updated — use its prefix for new UUIDs in responses.
					if string(f.Name()) == "id" {
						outputPrefix = opts.AliasPrefix
					}
				}
			}
			// Fallback: if no "id" field, use the first alias prefix
			if outputPrefix == "" && len(aliasFields) > 0 {
				outputPrefix = aliasFields[0].AliasPrefix
			}

			// The response is the source of truth for what gets registered.
			// The primary prefix comes from the response's own entity (e.g.
			// CreateLinkResponse.link.id -> "link"), and only falls back to the
			// request-derived prefix when the response declares none.
			responsePrefixes := collectResponseFieldPrefixes(method.Output.Desc)
			if p := responsePrimaryPrefix(method.Output.Desc); p != "" {
				outputPrefix = p
			}

			c.tools = append(c.tools, ToolInfo{
				VarName:        snakeToPascal(td.Name),
				Name:           td.Name,
				Description:    td.Description,
				Strict:         td.Strict,
				Schema:         schemaJSON,
				AutoExecute:    td.AutoExecute,
				ServiceName:    string(method.Desc.Parent().Name()),
				MethodName:     string(method.Desc.Name()),
				InputTypeName:  method.Input.GoIdent.GoName,
				OutputTypeName: method.Output.GoIdent.GoName,
				GoImportPath:   string(method.Input.GoIdent.GoImportPath),
				AliasFields:    aliasFields,
				OutputPrefix:   outputPrefix,

				ResponseFieldPrefixes: responsePrefixes,
			})
		}
	}
}

// primaryPrefixSentinel is the value stored for the ambiguous "id" JSON field:
// an empty prefix tells the runtime to use the tool's primary prefix.
const primaryPrefixSentinel = ""

// collectResponseFieldPrefixes walks a response message (recursively, through
// nested and repeated messages) and returns a map of camelCase JSON field name
// to alias prefix, as declared by (ai.tools.v1.tool_field).alias_prefix.
//
// Responses are marshaled with protojson, which emits JSON names, so the JSON
// name is the key the runtime walker will see.
func collectResponseFieldPrefixes(msg protoreflect.MessageDescriptor) map[string]string {
	out := make(map[string]string)
	visited := make(map[protoreflect.FullName]bool)
	walkResponseFields(msg, visited, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func walkResponseFields(msg protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, out map[string]string) {
	if msg == nil {
		return
	}
	name := msg.FullName()
	if visited[name] || strings.HasPrefix(string(name), "google.protobuf.") {
		return
	}
	visited[name] = true

	fields := msg.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)

		if prefix := annotations.GetToolFieldOpts(f).AliasPrefix; prefix != "" {
			jsonName := f.JSONName()
			if jsonName == "id" {
				// A bare "id" is ambiguous — it means a different entity
				// depending on where it sits in the response. The runtime uses
				// the tool's primary prefix for it.
				out[jsonName] = primaryPrefixSentinel
			} else if _, exists := out[jsonName]; !exists {
				out[jsonName] = prefix
			}
		}

		if f.IsMap() {
			if v := f.MapValue(); v.Kind() == protoreflect.MessageKind || v.Kind() == protoreflect.GroupKind {
				walkResponseFields(v.Message(), visited, out)
			}
			continue
		}

		if f.Kind() == protoreflect.MessageKind || f.Kind() == protoreflect.GroupKind {
			walkResponseFields(f.Message(), visited, out)
		}
	}
}

// responsePrimaryPrefix derives the prefix of the primary entity a response
// carries: the alias prefix on the "id" field of the top-level entity.
// e.g. CreateLinkResponse.link.id -> "link";
// ListWorkflowStepsResponse.workflow_steps[].id -> "step".
// Returns "" when the response declares no such field.
func responsePrimaryPrefix(msg protoreflect.MessageDescriptor) string {
	if msg == nil {
		return ""
	}
	fields := msg.Fields()

	// A top-level "id" on the response itself wins.
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.JSONName() == "id" {
			if prefix := annotations.GetToolFieldOpts(f).AliasPrefix; prefix != "" {
				return prefix
			}
		}
	}

	// Otherwise, the "id" of the first nested entity that declares one.
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.IsMap() || (f.Kind() != protoreflect.MessageKind && f.Kind() != protoreflect.GroupKind) {
			continue
		}
		nested := f.Message()
		if nested == nil || strings.HasPrefix(string(nested.FullName()), "google.protobuf.") {
			continue
		}
		nestedFields := nested.Fields()
		for j := 0; j < nestedFields.Len(); j++ {
			nf := nestedFields.Get(j)
			if nf.JSONName() != "id" {
				continue
			}
			if prefix := annotations.GetToolFieldOpts(nf).AliasPrefix; prefix != "" {
				return prefix
			}
		}
	}
	return ""
}

// Generate produces Go source code with all collected tool definitions.
// Returns empty string if no tools were collected.
func (c *Collector) Generate() string {
	if len(c.tools) == 0 {
		return ""
	}

	// Sort tools by name for deterministic output.
	sort.Slice(c.tools, func(i, j int) bool {
		return c.tools[i].Name < c.tools[j].Name
	})

	// Collect auto_execute tools and their import paths.
	var autoTools []ToolInfo
	importPaths := make(map[string]bool)
	for _, t := range c.tools {
		if t.AutoExecute {
			autoTools = append(autoTools, t)
			importPaths[t.GoImportPath] = true
		}
	}
	hasExecutors := len(autoTools) > 0

	// Check if any tool participates in aliasing, either on the request side
	// (alias resolution) or the response side (alias registration).
	hasAliases := false
	for _, t := range c.tools {
		if len(t.AliasFields) > 0 || len(t.ResponseFieldPrefixes) > 0 || t.OutputPrefix != "" {
			hasAliases = true
			break
		}
	}

	var b strings.Builder

	b.WriteString("// Code generated by protoc-gen-ai-tools. DO NOT EDIT.\n")
	b.WriteString("package gentools\n\n")

	// Imports block.
	if hasExecutors {
		b.WriteString("import (\n")
		b.WriteString("\t\"context\"\n")
		b.WriteString("\t\"encoding/json\"\n")
		b.WriteString("\t\"fmt\"\n")
		if hasAliases {
			b.WriteString("\t\"regexp\"\n")
			b.WriteString("\t\"sort\"\n")
			b.WriteString("\t\"strings\"\n")
			b.WriteString("\t\"sync\"\n")
		}
		b.WriteString("\n")
		b.WriteString("\t\"google.golang.org/grpc\"\n")
		b.WriteString("\t\"google.golang.org/protobuf/encoding/protojson\"\n")
		b.WriteString("\n")

		// Deduplicate and sort import paths for deterministic output.
		var paths []string
		for p := range importPaths {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			b.WriteString(fmt.Sprintf("\tpb %q\n", p))
		}
		b.WriteString(")\n\n")
	} else if hasAliases {
		b.WriteString("import (\n")
		b.WriteString("\t\"encoding/json\"\n")
		b.WriteString("\t\"fmt\"\n")
		b.WriteString("\t\"regexp\"\n")
		b.WriteString("\t\"sort\"\n")
		b.WriteString("\t\"strings\"\n")
		b.WriteString("\t\"sync\"\n")
		b.WriteString(")\n\n")
	} else {
		b.WriteString("import \"encoding/json\"\n\n")
	}

	b.WriteString("// AgentTool is a library-agnostic AI agent tool definition.\n")
	b.WriteString("type AgentTool struct {\n")
	b.WriteString("\tName        string\n")
	b.WriteString("\tDescription string\n")
	b.WriteString("\tStrict      bool\n")
	b.WriteString("\tParameters  json.RawMessage\n")
	b.WriteString("}\n")

	// Generate individual tool variables.
	for _, t := range c.tools {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("var %s = AgentTool{\n", t.VarName))
		b.WriteString(fmt.Sprintf("\tName:        %q,\n", t.Name))
		b.WriteString(fmt.Sprintf("\tDescription: %q,\n", t.Description))
		b.WriteString(fmt.Sprintf("\tStrict:      %t,\n", t.Strict))
		b.WriteString(fmt.Sprintf("\tParameters:  json.RawMessage(%q),\n", string(t.Schema)))
		b.WriteString("}\n")
	}

	// Generate AllTools function.
	b.WriteString("\n// AllTools returns all generated tool definitions.\n")
	b.WriteString("func AllTools() []AgentTool {\n")
	b.WriteString("\treturn []AgentTool{\n")
	for _, t := range c.tools {
		b.WriteString(fmt.Sprintf("\t\t%s,\n", t.VarName))
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n")

	// Generate AliasManager if any tool has alias fields.
	if hasAliases {
		c.generateAliasManager(&b)
	}

	// Generate executor code if any tools have auto_execute.
	if hasExecutors {
		// Generate toolAliasPrefixes map if aliases exist.
		if hasAliases {
			c.generateToolAliasPrefixes(&b, autoTools)
		}

		b.WriteString("\n// Executor provides auto-generated tool executors backed by gRPC.\n")
		b.WriteString("type Executor struct {\n")
		b.WriteString("\tconn grpc.ClientConnInterface\n")
		if hasAliases {
			b.WriteString("\taliases *AliasManager\n")
		}
		b.WriteString("}\n")

		b.WriteString("\n// NewExecutor creates an Executor with the given gRPC connection.\n")
		b.WriteString("func NewExecutor(conn grpc.ClientConnInterface) *Executor {\n")
		if hasAliases {
			b.WriteString("\treturn &Executor{conn: conn, aliases: NewAliasManager()}\n")
		} else {
			b.WriteString("\treturn &Executor{conn: conn}\n")
		}
		b.WriteString("}\n")

		if hasAliases {
			b.WriteString("\n// GetAliasManager returns the alias manager for external use (e.g. manual tools).\n")
			b.WriteString("func (e *Executor) GetAliasManager() *AliasManager {\n")
			b.WriteString("\treturn e.aliases\n")
			b.WriteString("}\n")
		}

		// Generate individual executor methods.
		for _, t := range autoTools {
			funcName := "Execute" + snakeToPascal(t.Name)
			b.WriteString(fmt.Sprintf("\n// %s calls %s.%s via gRPC.\n", funcName, t.ServiceName, t.MethodName))
			b.WriteString(fmt.Sprintf("func (e *Executor) %s(ctx context.Context, argsJSON string) (string, error) {\n", funcName))
			if hasAliases {
				b.WriteString(fmt.Sprintf("\targsJSON = e.aliases.ResolveJSON(%q, argsJSON)\n", t.Name))
			}
			b.WriteString(fmt.Sprintf("\tvar req pb.%s\n", t.InputTypeName))
			b.WriteString("\topts := protojson.UnmarshalOptions{DiscardUnknown: true}\n")
			b.WriteString("\tif err := opts.Unmarshal([]byte(argsJSON), &req); err != nil {\n")
			b.WriteString("\t\treturn \"\", fmt.Errorf(\"failed to parse arguments: %w\", err)\n")
			b.WriteString("\t}\n")
			b.WriteString(fmt.Sprintf("\tclient := pb.New%sClient(e.conn)\n", t.ServiceName))
			b.WriteString(fmt.Sprintf("\tresp, err := client.%s(ctx, &req)\n", t.MethodName))
			b.WriteString("\tif err != nil {\n")
			b.WriteString("\t\treturn \"\", err\n")
			b.WriteString("\t}\n")
			b.WriteString("\tmopts := protojson.MarshalOptions{EmitUnpopulated: false}\n")
			b.WriteString("\tout, err := mopts.Marshal(resp)\n")
			b.WriteString("\tif err != nil {\n")
			b.WriteString("\t\treturn \"\", fmt.Errorf(\"failed to marshal response: %w\", err)\n")
			b.WriteString("\t}\n")
			if hasAliases {
				b.WriteString(fmt.Sprintf("\te.aliases.RegisterFieldsFromJSON(toolOutputPrefix[%q], string(out))\n", t.Name))
				b.WriteString(fmt.Sprintf("\treturn e.aliases.AliasifyJSON(%q, string(out)), nil\n", t.Name))
			} else {
				b.WriteString("\treturn string(out), nil\n")
			}
			b.WriteString("}\n")
		}

		// Generate CanExecute method.
		b.WriteString("\n// CanExecute returns true if the given tool name has an auto-generated executor.\n")
		b.WriteString("func (e *Executor) CanExecute(toolName string) bool {\n")
		b.WriteString("\tswitch toolName {\n")
		for _, t := range autoTools {
			b.WriteString(fmt.Sprintf("\tcase %q:\n", t.Name))
			b.WriteString("\t\treturn true\n")
		}
		b.WriteString("\tdefault:\n")
		b.WriteString("\t\treturn false\n")
		b.WriteString("\t}\n")
		b.WriteString("}\n")

		// Generate Execute dispatcher method.
		b.WriteString("\n// Execute dispatches to the correct executor by tool name.\n")
		b.WriteString("func (e *Executor) Execute(ctx context.Context, toolName string, argsJSON string) (string, error) {\n")
		b.WriteString("\tswitch toolName {\n")
		for _, t := range autoTools {
			funcName := "Execute" + snakeToPascal(t.Name)
			b.WriteString(fmt.Sprintf("\tcase %q:\n", t.Name))
			b.WriteString(fmt.Sprintf("\t\treturn e.%s(ctx, argsJSON)\n", funcName))
		}
		b.WriteString("\tdefault:\n")
		b.WriteString("\t\treturn \"\", fmt.Errorf(\"no auto-executor for tool: %s\", toolName)\n")
		b.WriteString("\t}\n")
		b.WriteString("}\n")
	}

	src := b.String()

	// Normalize alignment/spacing so the output is gofmt-clean regardless of
	// how the fragments above are spaced. If it somehow does not parse, fall
	// back to the raw source so the failure is visible in the generated file.
	if formatted, err := format.Source([]byte(src)); err == nil {
		src = string(formatted)
	}

	return strings.TrimRight(src, "\n")
}

// generateAliasManager writes the AliasManager struct and methods.
func (c *Collector) generateAliasManager(b *strings.Builder) {
	b.WriteString("\n// AliasManager maps UUIDs to human-readable aliases and back.\n")
	b.WriteString("// The LLM never sees raw UUIDs — only aliases like \"link-1\", \"step-2\".\n")
	b.WriteString("// Thread-safe for concurrent use within a single conversation.\n")
	b.WriteString("type AliasManager struct {\n")
	b.WriteString("\tmu       sync.Mutex\n")
	b.WriteString("\tcounters map[string]int            // prefix → next counter\n")
	b.WriteString("\ttoAlias  map[string]string         // UUID → alias\n")
	b.WriteString("\ttoUUID   map[string]string         // alias → UUID\n")
	b.WriteString("}\n")

	b.WriteString("\n// NewAliasManager creates a new AliasManager.\n")
	b.WriteString("func NewAliasManager() *AliasManager {\n")
	b.WriteString("\treturn &AliasManager{\n")
	b.WriteString("\t\tcounters: make(map[string]int),\n")
	b.WriteString("\t\ttoAlias:  make(map[string]string),\n")
	b.WriteString("\t\ttoUUID:   make(map[string]string),\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")

	b.WriteString("\n// Register assigns an alias to a UUID. If the UUID already has an alias, returns it.\n")
	b.WriteString("func (am *AliasManager) Register(prefix string, uuid string) string {\n")
	b.WriteString("\tam.mu.Lock()\n")
	b.WriteString("\tdefer am.mu.Unlock()\n")
	b.WriteString("\tif alias, ok := am.toAlias[uuid]; ok {\n")
	b.WriteString("\t\treturn alias\n")
	b.WriteString("\t}\n")
	b.WriteString("\tam.counters[prefix]++\n")
	b.WriteString("\talias := fmt.Sprintf(\"%s-%d\", prefix, am.counters[prefix])\n")
	b.WriteString("\tam.toAlias[uuid] = alias\n")
	b.WriteString("\tam.toUUID[alias] = uuid\n")
	b.WriteString("\treturn alias\n")
	b.WriteString("}\n")

	b.WriteString("\n// ResolveAlias returns the UUID for an alias. If not found, returns the input unchanged.\n")
	b.WriteString("func (am *AliasManager) ResolveAlias(alias string) string {\n")
	b.WriteString("\tam.mu.Lock()\n")
	b.WriteString("\tdefer am.mu.Unlock()\n")
	b.WriteString("\tif uuid, ok := am.toUUID[alias]; ok {\n")
	b.WriteString("\t\treturn uuid\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn alias\n")
	b.WriteString("}\n")

	b.WriteString("\n// AliasifyJSON replaces UUID values with aliases in a JSON string.\n")
	b.WriteString("func (am *AliasManager) AliasifyJSON(toolName string, jsonStr string) string {\n")
	b.WriteString("\tam.mu.Lock()\n")
	b.WriteString("\tdefer am.mu.Unlock()\n")
	b.WriteString("\tfor uuid, alias := range am.toAlias {\n")
	b.WriteString("\t\tjsonStr = strings.ReplaceAll(jsonStr, uuid, alias)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn jsonStr\n")
	b.WriteString("}\n")

	b.WriteString("\n// ResolveJSON replaces alias values with UUIDs in a JSON string.\n")
	b.WriteString("func (am *AliasManager) ResolveJSON(toolName string, jsonStr string) string {\n")
	b.WriteString("\tam.mu.Lock()\n")
	b.WriteString("\tdefer am.mu.Unlock()\n")
	b.WriteString("\tfor alias, uuid := range am.toUUID {\n")
	b.WriteString("\t\tjsonStr = strings.ReplaceAll(jsonStr, alias, uuid)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn jsonStr\n")
	b.WriteString("}\n")

	b.WriteString("\nvar uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)\n")

	b.WriteString("\n// RegisterNewUUIDs scans a JSON string for UUID patterns and registers every\n")
	b.WriteString("// match under a single prefix.\n")
	b.WriteString("//\n")
	b.WriteString("// Deprecated: this cannot tell a link id from a workspace id and aliases both\n")
	b.WriteString("// under the same prefix. Generated executors use RegisterFieldsFromJSON.\n")
	b.WriteString("// Kept for callers that register UUIDs from non-proto sources.\n")
	b.WriteString("func (am *AliasManager) RegisterNewUUIDs(prefix string, jsonStr string) {\n")
	b.WriteString("\tfor _, uuid := range uuidPattern.FindAllString(jsonStr, -1) {\n")
	b.WriteString("\t\tam.Register(prefix, uuid)\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")

	c.generateResponseFieldPrefixes(b)

	b.WriteString("\n// RegisterFieldsFromJSON walks a JSON response and registers UUIDs using the\n")
	b.WriteString("// prefix declared for the field that holds them. Fields missing from\n")
	b.WriteString("// responseFieldPrefixes are skipped, so unrelated UUIDs (workspace ids,\n")
	b.WriteString("// design ids, ...) never get an alias of the wrong entity.\n")
	b.WriteString("//\n")
	b.WriteString("// primaryPrefix is used for fields mapped to an empty prefix (the bare \"id\").\n")
	b.WriteString("func (am *AliasManager) RegisterFieldsFromJSON(primaryPrefix string, jsonStr string) {\n")
	b.WriteString("\tvar data any\n")
	b.WriteString("\tif err := json.Unmarshal([]byte(jsonStr), &data); err != nil {\n")
	b.WriteString("\t\treturn\n")
	b.WriteString("\t}\n")
	b.WriteString("\tam.registerValue(primaryPrefix, \"\", data)\n")
	b.WriteString("}\n")

	b.WriteString("\n// registerValue recurses through a decoded JSON value, carrying down the name\n")
	b.WriteString("// of the field the value was found under.\n")
	b.WriteString("func (am *AliasManager) registerValue(primaryPrefix string, fieldName string, value any) {\n")
	b.WriteString("\tswitch v := value.(type) {\n")
	b.WriteString("\tcase map[string]any:\n")
	b.WriteString("\t\t// Sort keys so alias numbering is deterministic.\n")
	b.WriteString("\t\tkeys := make([]string, 0, len(v))\n")
	b.WriteString("\t\tfor k := range v {\n")
	b.WriteString("\t\t\tkeys = append(keys, k)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tsort.Strings(keys)\n")
	b.WriteString("\t\tfor _, k := range keys {\n")
	b.WriteString("\t\t\tam.registerValue(primaryPrefix, k, v[k])\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\tcase []any:\n")
	b.WriteString("\t\t// Repeated values keep the field name of the list itself.\n")
	b.WriteString("\t\tfor _, item := range v {\n")
	b.WriteString("\t\t\tam.registerValue(primaryPrefix, fieldName, item)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\tcase string:\n")
	b.WriteString("\t\tif fieldName == \"\" || uuidPattern.FindString(v) != v {\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tprefix, ok := responseFieldPrefixes[fieldName]\n")
	b.WriteString("\t\tif !ok {\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif prefix == \"\" {\n")
	b.WriteString("\t\t\tprefix = primaryPrefix\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif prefix == \"\" {\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tam.Register(prefix, v)\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
}

// generateToolAliasPrefixes writes the toolOutputPrefix map variable.
func (c *Collector) generateToolAliasPrefixes(b *strings.Builder, autoTools []ToolInfo) {
	b.WriteString("\n// toolOutputPrefix maps tool names to the primary prefix of the entity the\n")
	b.WriteString("// tool returns, derived from the id field of the top-level entity in the\n")
	b.WriteString("// response message. It is used for response fields whose prefix in\n")
	b.WriteString("// responseFieldPrefixes is empty (i.e. the bare \"id\" field).\n")
	b.WriteString("var toolOutputPrefix = map[string]string{\n")
	for _, t := range autoTools {
		if t.OutputPrefix != "" {
			b.WriteString(fmt.Sprintf("\t%q: %q,\n", t.Name, t.OutputPrefix))
		}
	}
	b.WriteString("}\n")
}

// mergedResponseFieldPrefixes merges every tool's response field prefix map
// into a single package-level map. Tools are already sorted by name, so the
// merge (first non-conflicting entry wins) is deterministic.
func (c *Collector) mergedResponseFieldPrefixes() map[string]string {
	merged := make(map[string]string)
	for _, t := range c.tools {
		for field, prefix := range t.ResponseFieldPrefixes {
			if prefix == primaryPrefixSentinel {
				// The ambiguous "id" always defers to the tool's primary prefix.
				merged[field] = primaryPrefixSentinel
				continue
			}
			if _, exists := merged[field]; !exists {
				merged[field] = prefix
			}
		}
	}
	return merged
}

// generateResponseFieldPrefixes writes the responseFieldPrefixes map variable.
func (c *Collector) generateResponseFieldPrefixes(b *strings.Builder) {
	merged := c.mergedResponseFieldPrefixes()

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	b.WriteString("\n// responseFieldPrefixes maps a response JSON field name to the alias prefix\n")
	b.WriteString("// declared for it in the proto. Responses are marshaled with protojson, so\n")
	b.WriteString("// keys are camelCase JSON names.\n")
	b.WriteString("//\n")
	b.WriteString("// An empty prefix means \"use the tool's primary prefix\" (see toolOutputPrefix).\n")
	b.WriteString("// Fields absent from this map are never aliased.\n")
	b.WriteString("var responseFieldPrefixes = map[string]string{\n")
	for _, name := range names {
		if merged[name] == primaryPrefixSentinel {
			b.WriteString(fmt.Sprintf("\t%q: %q, // use the tool's primary prefix\n", name, merged[name]))
			continue
		}
		b.WriteString(fmt.Sprintf("\t%q: %q,\n", name, merged[name]))
	}
	b.WriteString("}\n")
}

// snakeToPascal converts a snake_case string to PascalCase.
func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]))
		if len(part) > 1 {
			result.WriteString(part[1:])
		}
	}
	return result.String()
}
