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

## Companion plugin

This plugin pairs with [protoc-gen-ai-context](https://github.com/Loschcode/protoc-gen-ai-context), which generates **knowledge markdown** from proto annotations. Together they make proto files the single source of truth for AI agent behavior:

- `protoc-gen-ai-context` → `.ai.md` knowledge files (how things work)
- `protoc-gen-ai-tools` → `.gen.go` tool definitions (what actions are available)

Note: `protoc-gen-ai-context` has its own `ai_skip` annotation for excluding fields from generated markdown. That is separate from this plugin's `tool_field` annotation — `tool_field` lives in `ai/tools/v1/annotations.proto` and only affects the generated tool schema, not the context markdown.

## License

MIT
