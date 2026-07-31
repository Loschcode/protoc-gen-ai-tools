# protoc-gen-ai-tools

A protoc plugin that generates **Go tool definitions** from proto RPC annotations. The generated tools include full JSON Schema parameters derived from the request message, ready to plug into any LLM (OpenAI, Claude, Gemini, etc.).

Your proto files become the single source of truth for AI tool definitions — no more hand-writing JSON schemas in application code.

## Install

```bash
go install github.com/Loschcode/protoc-gen-ai-tools/cmd/protoc-gen-ai-tools@latest
```

## How it works

1. Annotate your RPC methods with `(ai.context.v1.rpc_tool)`
2. Run `protoc` (or `buf generate`)
3. Get a Go file with typed tool definitions and JSON Schema parameters
4. Pass the tools to any LLM — the generated `AgentTool` struct is library-agnostic

```
Proto RPC + Request Message  →  protoc-gen-ai-tools  →  Go code with AgentTool + JSON Schema
```

## Usage

### 1. Define the annotation

Add the annotation proto to your project (shared with [protoc-gen-ai-context](https://github.com/Loschcode/protoc-gen-ai-context)):

```proto
// ai/context/v1/annotations.proto
syntax = "proto3";
package ai.context.v1;

import "google/protobuf/descriptor.proto";

message ToolDefinition {
  string name = 1;
  string description = 2;
  bool strict = 3;
}

extend google.protobuf.MethodOptions {
  ToolDefinition rpc_tool = 52104;
}
```

### 2. Annotate your RPCs

```proto
import "ai/context/v1/annotations.proto";

message ThemeColor {
  enum ThemeColorType {
    THEME_COLOR_TYPE_UNSPECIFIED = 0;
    THEME_COLOR_TYPE_SOLID = 1;
    THEME_COLOR_TYPE_LINEAR = 2;
  }
  ThemeColorType type = 1;
  repeated string colors = 2;
  optional string direction = 3;
}

message UpdatePageThemeRequest {
  string id = 1;
  optional ThemeColor canvas_color = 2;
  optional ThemeColor element_color = 3;
  optional bool container_enabled = 4;
}

service PageThemesService {
  rpc UpdatePageTheme(UpdatePageThemeRequest) returns (UpdatePageThemeResponse) {
    option (ai.context.v1.rpc_tool) = {
      name: "page_themes_update"
      description: "Update page theme colors and styling."
      strict: true
    };
  };
}
```

### 3. Generate

With buf:
```yaml
# buf.gen.yaml
plugins:
  - name: ai-tools
    out: internal/agents/gentools
```

Or with protoc:
```bash
protoc --ai-tools_out=internal/agents/gentools your_service.proto
```

### 4. Use the generated tools

The plugin generates `ai_tools.gen.go`:

```go
package gentools

import "encoding/json"

type AgentTool struct {
    Name        string
    Description string
    Strict      bool
    Parameters  json.RawMessage
}

var PageThemesUpdate = AgentTool{
    Name:        "page_themes_update",
    Description: "Update page theme colors and styling.",
    Strict:      true,
    Parameters:  json.RawMessage(`{"additionalProperties":false,"properties":{"id":{"type":"string"},"canvas_color":{"properties":{"type":{"enum":["THEME_COLOR_TYPE_SOLID","THEME_COLOR_TYPE_LINEAR"],"type":"string"},"colors":{"items":{"type":"string"},"type":"array"},"direction":{"type":"string"}},"type":"object"},...},"required":["id"],"type":"object"}`),
}

func AllTools() []AgentTool {
    return []AgentTool{PageThemesUpdate}
}
```

## Plugging into LLMs

The generated `AgentTool` is library-agnostic. Here's how to convert it for each provider:

### OpenAI (Responses API)

```go
import (
    "gentools"
    "github.com/openai/openai-go/v3/responses"
    "github.com/openai/openai-go/v3/packages/param"
)

func toOpenAI(t gentools.AgentTool) responses.ToolUnionParam {
    var schema map[string]any
    json.Unmarshal(t.Parameters, &schema)
    return responses.ToolUnionParam{
        OfFunction: &responses.FunctionToolParam{
            Name:        t.Name,
            Description: param.NewOpt(t.Description),
            Strict:      param.NewOpt(t.Strict),
            Parameters:  schema,
        },
    }
}
```

### Anthropic Claude

```go
import "github.com/anthropics/anthropic-sdk-go"

func toClaude(t gentools.AgentTool) anthropic.ToolParam {
    var schema anthropic.ToolInputSchemaParam
    json.Unmarshal(t.Parameters, &schema)
    return anthropic.ToolParam{
        Name:        t.Name,
        Description: anthropic.String(t.Description),
        InputSchema: schema,
    }
}
```

### Raw JSON (any provider)

```go
func toJSON(t gentools.AgentTool) map[string]any {
    return map[string]any{
        "type": "function",
        "function": map[string]any{
            "name":        t.Name,
            "description": t.Description,
            "parameters":  json.RawMessage(t.Parameters),
            "strict":      t.Strict,
        },
    }
}
```

## Schema generation rules

| Proto Type | JSON Schema |
|---|---|
| `string` | `{"type": "string"}` |
| `int32`, `int64`, etc. | `{"type": "integer"}` |
| `float`, `double` | `{"type": "number"}` |
| `bool` | `{"type": "boolean"}` |
| `bytes` | `{"type": "string", "format": "byte"}` |
| `repeated X` | `{"type": "array", "items": ...}` |
| `map<K, V>` | `{"type": "object", "additionalProperties": ...}` |
| Nested message | `{"type": "object", "properties": ...}` (recursive) |
| Enum | `{"type": "string", "enum": [...]}` (skips `_UNSPECIFIED = 0`) |
| `google.protobuf.Timestamp` | `{"type": "string", "format": "date-time"}` |
| `optional` fields | Excluded from `required` |
| `OUTPUT_ONLY` fields | Skipped entirely |

When `strict: true`, all objects get `"additionalProperties": false`.

### Field guidance for the model only

A field's leading comment is read by three audiences at once: developers using the generated structs, consumers of your OpenAPI spec, and the model. Behavioural coaching written there ships into your public API reference:

```proto
// The shortlink for the link.
// Send this empty unless the user asked for one — inventing a slug risks a
// collision and fails the creation.        // ← now in your public swagger
string shortlink = 4;
```

Keep the comment factual and put the coaching in `usage_notes`, which is appended to the description in the tool schema and nowhere else:

```proto
// The shortlink (URL slug) for the link.
string shortlink = 4 [(ai.tools.v1.tool_field) = {
  usage_notes: "Send an empty string unless the user explicitly asked for a specific slug. The system generates a unique one by default."
}];
```

The model sees both parts, separated by a blank line; OpenAPI and the Go structs see only the comment.

`description` replaces the comment outright for the model. Prefer `usage_notes` — it keeps the factual half shared. Reach for `description` only when the public wording would actively mislead a model:

```proto
string shortlink = 4 [(ai.tools.v1.tool_field) = {
  description: "URL slug. Leave empty."
}];
```

Precedence is `description ?? leading comment`, then `usage_notes` appended.

This is deliberately the same word `usage_notes` that [protoc-gen-ai-context](https://github.com/Loschcode/protoc-gen-ai-context) uses on `message_knowledge` — message level there, field level here.

When a field sets more than one option, the aggregate form reads better than repeated dotted assignments:

```proto
string link_id = 1 [(ai.tools.v1.tool_field) = {
  alias_prefix: "link"
  usage_notes: "Use the id returned by links_create; never invent one."
}];
```

### Skipping fields from the AI schema

Some fields exist in the gRPC API but shouldn't be exposed to the AI (e.g., `map` fields with dynamic keys that are incompatible with strict mode, or developer-facing fields that end users never interact with).

Use the `tool_field` annotation to exclude them:

```proto
import "ai/tools/v1/annotations.proto";

message CreateLinkRequest {
  string destination = 1;
  string name = 2;
  repeated string tags = 3;
  
  // Developer-facing metadata — skip from AI tool schema
  map<string, string> metadata = 4 [(ai.tools.v1.tool_field).skip = true];
}
```

The field remains fully available in the gRPC/REST API and SDKs — only the AI tool definition omits it. This is particularly useful for:
- `map<K,V>` fields (incompatible with strict mode's `additionalProperties: false`)
- Internal/debug fields the AI has no use for
- Fields with complex types that confuse the LLM

### Transparent UUID aliasing

LLMs struggle with raw UUIDs — they copy them wrong, mix them up, or invent fake ones. The `alias_prefix` annotation solves this by transparently replacing UUIDs with human-readable aliases.

Annotate `alias_prefix` on **both** sides:
- on **request** fields, so aliases coming from the LLM resolve back to UUIDs
- on **response** fields, so UUIDs coming back from gRPC are registered under the right prefix

```proto
message Link {
  // The entity's own id — appears as "link-1", "link-2", ... to the LLM
  string id = 1 [(ai.tools.v1.tool_field).alias_prefix = "link"];

  // Cross-entity reference — separate counter: "theme-1", "theme-2", ...
  string page_theme_id = 2 [(ai.tools.v1.tool_field).alias_prefix = "theme"];

  // No annotation → never aliased, stays a raw UUID
  string workspace_id = 3;
}

message CreateLinkResponse {
  Link link = 1;
}

message CreateWorkflowStepRequest {
  // Resolves against the same "link" alias map as Link.id
  string link_id = 1 [(ai.tools.v1.tool_field).alias_prefix = "link"];
}
```

The aliasing is **fully mechanical** — no prompt engineering needed:

```
gRPC response: {"id": "d290f1ee-6c54-4b01-90e6-d701748f0851"}
                            ↓ AliasManager (UUID → alias)
LLM sees:      {"id": "link-1"}

LLM responds:  {"link_id": "link-1", ...}
                            ↓ AliasManager (alias → UUID)
gRPC request:  {"link_id": "d290f1ee-6c54-4b01-90e6-d701748f0851", ...}
```

How it works:
- **Outbound** (gRPC response → LLM): the response JSON is walked **field by field**. Each UUID is registered under the prefix declared on *its own field*, then replaced with the alias.
- **Inbound** (LLM → gRPC request): all aliases in the args are resolved back to UUIDs before calling gRPC
- Fields with the **same prefix** share the same alias counter — so `link_id` on `CreateWorkflowStepRequest` resolves against the same map as `Link.id`
- Each `Executor` owns its own `AliasManager`. It is thread-safe, but it is **not** scoped for you — see below.

### Scope one executor per conversation

Aliases are sequential and per-manager: the first link registered is `link-1`, the next `link-2`. That numbering is only meaningful inside one conversation.

**Do not create a single process-wide executor.** If two conversations share an `AliasManager`, whichever registers first owns `link-1`, and the other conversation's `link-1` resolves to an entity it has nothing to do with — across users and tenants. The maps also grow for the lifetime of the process, since nothing evicts them.

```go
// WRONG — one alias namespace shared by every conversation and every user.
var executor = gentools.NewExecutor(conn)   // process-wide

// RIGHT — one executor per conversation; the gRPC connection is still shared.
executor := sessions.For(conversationID)
```

A minimal registry: key by conversation id, evict on an idle TTL so alias maps do not accumulate.

```go
type Sessions struct {
    conn grpc.ClientConnInterface
    ttl  time.Duration
    mu   sync.Mutex
    m    map[string]*entry
}

type entry struct {
    executor *gentools.Executor
    lastUsed time.Time
}

func (s *Sessions) For(conversationID string) *gentools.Executor {
    // An empty id gets a throwaway executor rather than sharing one, so a
    // missing id can never collapse separate conversations into one namespace.
    if conversationID == "" {
        return gentools.NewExecutor(s.conn)
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    s.evictExpiredLocked()
    if e, ok := s.m[conversationID]; ok {
        e.lastUsed = time.Now()
        return e.executor
    }
    e := &entry{executor: gentools.NewExecutor(s.conn), lastUsed: time.Now()}
    s.m[conversationID] = e
    return e.executor
}
```

Aliases must persist across *turns* of the same conversation — that is what makes them usable — so the executor has to outlive a single request. It just must not outlive the conversation.

### Applying aliases to hand-written tools

Generated executors resolve and aliasify internally. Tools you write by hand (a UI helper, a knowledge lookup, anything not backed by an annotated RPC) bypass that, so an alias handed to them arrives unresolved and any UUID they return leaks straight to the model.

Wrap them with the **same** `AliasManager` the generated tools use, so both sides share one namespace and IDs can move freely between manual and generated tools:

```go
executor := sessions.For(conversationID)
aliases := executor.GetAliasManager()

isGenerated := executor.CanExecute(toolName)
if !isGenerated {
    argsJSON = aliases.ResolveJSON(toolName, argsJSON)   // alias → UUID
}

result, err := dispatch(ctx, toolName, argsJSON)

if !isGenerated {
    result = aliases.AliasifyJSON(toolName, result)      // UUID → alias
}
```

Two things worth knowing:

- **Exclude display-only tools from inbound resolution.** If a tool's arguments are rendered to the user, resolving aliases there puts raw UUIDs on screen — the one place they must never appear.
- **Register ids the model never called a tool for.** Context or state injected into the prompt contains UUIDs the `AliasManager` has not seen. Register those up front with `Register(prefix, uuid)` and aliasify the prompt, or the model will encounter a raw UUID and start inventing others to match.

Registration is **field-aware**, driven by the response message shape. The plugin walks each RPC's response message (recursively, through nested and repeated fields) and emits two maps:

```go
// JSON field name (protojson camelCase) → alias prefix.
// "" means: use the tool's primary prefix.
var responseFieldPrefixes = map[string]string{
	"id":          "",
	"linkId":      "link",
	"pageThemeId": "theme",
}

// Tool → the prefix of the entity it returns, taken from the id field of the
// top-level entity in the RESPONSE message.
var toolOutputPrefix = map[string]string{
	"links_create":        "link", // CreateLinkResponse.link.id
	"workflow_steps_list": "step", // ListWorkflowStepsResponse.workflow_steps[].id
}
```

At runtime the executor calls `RegisterFieldsFromJSON(toolOutputPrefix[tool], responseJSON)`, which recurses through the decoded JSON and, for every UUID-valued string, looks up **the field it sits under**:

- field not in `responseFieldPrefixes` → **skipped**, never aliased (this is what keeps `workspaceId` out of the `link` counter)
- field mapped to a prefix → registered under that prefix (`pageThemeId` → `theme-1`)
- field mapped to `""` (the ambiguous bare `id`) → registered under the tool's primary prefix

Two consequences worth knowing:

- The primary prefix comes from the **response**, not the request. `workflow_steps_list` returns step ids even though its request only carries `link_id`, so its ids are registered as `step-N`, not `link-N`. It also keeps working when a request `id` field is `skip = true`.
- Only annotated fields are aliased. If a new UUID field in a response should be aliased, annotate it.

`AliasManager.RegisterNewUUIDs(prefix, json)` — the old regex-scan-everything behaviour — is still exported for callers registering UUIDs from non-proto sources, but generated executors no longer use it: it cannot tell a link id from a workspace id.

Access the alias manager directly (see "Applying aliases to hand-written tools" above):
```go
executor := sessions.For(conversationID)   // NOT a process-wide executor
aliasManager := executor.GetAliasManager()

// Register a UUID manually — e.g. an id that arrived via injected context
// rather than a tool response
alias := aliasManager.Register("link", "d290f1ee-...")  // returns "link-1"

// Resolve an alias
uuid := aliasManager.ResolveAlias("link-1")  // returns "d290f1ee-..."
```

Note that `ResolveAlias` returns an unrecognised input unchanged. A model that invents an alias (`step-0` is a common one, inferred from seeing `step-1` and `step-2`) will therefore send that literal string on to the API, which typically surfaces as an opaque "invalid UUID" error rather than anything the model can act on. If that matters to you, validate before dispatch and return a message naming the ids that are actually valid.

## Companion plugin

This plugin pairs with [protoc-gen-ai-context](https://github.com/Loschcode/protoc-gen-ai-context), which generates **knowledge markdown** from proto annotations. Together they make proto files the single source of truth for AI agent behavior:

- `protoc-gen-ai-context` → `.ai.md` knowledge files (how things work)
- `protoc-gen-ai-tools` → `.gen.go` tool definitions (what actions are available)

Note: `protoc-gen-ai-context` has its own `ai_skip` annotation for excluding fields from generated markdown. That is separate from this plugin's `tool_field` annotation — `tool_field` lives in `ai/tools/v1/annotations.proto` and only affects the generated tool schema, not the context markdown.

## License

MIT
