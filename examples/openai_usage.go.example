// Example: using generated tools with OpenAI's Responses API (openai-go/v3)
//
// This shows how the generated AgentTool definitions are converted and
// passed to OpenAI. The same pattern works with Claude, Gemini, or any
// LLM that accepts JSON Schema function definitions.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	// Import the generated tools package
	// "yourproject/internal/agents/gentools"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// AgentTool matches the generated type from protoc-gen-ai-tools.
// In real code, import it from gentools package.
type AgentTool struct {
	Name        string
	Description string
	Strict      bool
	Parameters  json.RawMessage
}

// Convert a generated AgentTool to OpenAI's tool format.
// This is the only glue code needed — everything else comes from proto.
func toOpenAITool(t AgentTool) responses.ToolUnionParam {
	var schema map[string]any
	_ = json.Unmarshal(t.Parameters, &schema)
	return responses.ToolUnionParam{
		OfFunction: &responses.FunctionToolParam{
			Name:        t.Name,
			Description: param.NewOpt(t.Description),
			Strict:      param.NewOpt(t.Strict),
			Parameters:  schema,
		},
	}
}

func main() {
	client := openai.NewClient(option.WithAPIKey("sk-..."))

	// In real code: tools := gentools.AllTools()
	// Here we simulate one tool for the example
	exampleTool := AgentTool{
		Name:        "page_themes_update",
		Description: "Update page theme colors and styling.",
		Strict:      true,
		Parameters: json.RawMessage(`{
			"type": "object",
			"additionalProperties": false,
			"required": ["id"],
			"properties": {
				"id": {"type": "string"},
				"canvas_color": {
					"type": "object",
					"properties": {
						"type": {"type": "string", "enum": ["THEME_COLOR_TYPE_SOLID", "THEME_COLOR_TYPE_LINEAR"]},
						"colors": {"type": "array", "items": {"type": "string"}}
					}
				}
			}
		}`),
	}

	// Convert all tools
	var openaiTools []responses.ToolUnionParam
	openaiTools = append(openaiTools, toOpenAITool(exampleTool))

	// Call the API
	resp, err := client.Responses.New(context.Background(), responses.ResponseNewParams{
		Model:        "gpt-4o",
		Instructions: param.NewOpt("You are a helpful assistant."),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: param.NewOpt("Make the background dark blue"),
		},
		Tools: openaiTools,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Handle tool calls
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			fmt.Printf("Tool: %s\nArgs: %s\n", item.Name, item.Arguments.OfString)
			// The args will match the proto schema exactly —
			// nested ThemeColor objects, enum strings, etc.
		}
	}

	fmt.Println("Response:", resp.OutputText())
}
