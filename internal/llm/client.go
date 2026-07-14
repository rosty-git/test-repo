// Package llm provides a minimal, provider-agnostic interface for
// structured JSON calls, plus an Anthropic implementation. Generation and
// evaluation code depends only on the Client interface, so swapping in
// another provider (OpenAI, etc.) means writing one new file here.
package llm

import (
	"context"
	"encoding/json"
)

// ToolRequest asks the model to produce output matching ToolSchema. Using
// a forced tool call (rather than "please reply with JSON") is what makes
// the structured output reliable enough to parse without a repair step.
type ToolRequest struct {
	System      string
	User        string
	ToolName    string
	ToolDesc    string
	ToolSchema  map[string]any
	MaxTokens   int
	Temperature float64
}

type Client interface {
	// CallTool returns the raw JSON input the model produced for the
	// forced tool call, matching ToolSchema.
	CallTool(ctx context.Context, req ToolRequest) (json.RawMessage, error)
}
