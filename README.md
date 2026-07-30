# protoc-gen-ai-tools

A protoc plugin that generates Go code with AI agent tool definitions from proto RPC annotations.

## Install

```bash
go install github.com/Loschcode/protoc-gen-ai-tools/cmd/protoc-gen-ai-tools@latest
```

## Usage

Annotate your RPCs with `rpc_tool`:

```proto
import "ai/context/v1/annotations.proto";

service MyService {
  rpc UpdateTheme(UpdateThemeRequest) returns (UpdateThemeResponse) {
    option (ai.context.v1.rpc_tool) = {
      name: "page_themes_update"
      description: "Update a page theme's visual settings."
      strict: true
    };
  }
}
```

Run protoc with this plugin to generate `ai_tools.gen.go` containing typed `AgentTool` definitions with JSON Schema parameters.
