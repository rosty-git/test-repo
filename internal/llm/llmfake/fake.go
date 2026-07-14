// Package llmfake is a scripted implementation of llm.Client for tests:
// no network access, deterministic, and it records every call so tests can
// assert on prompt content (e.g. that retry feedback actually reached the
// next prompt).
package llmfake

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rosty-git/test-repo/internal/llm"
)

type Client struct {
	mu    sync.Mutex
	queue map[string][]json.RawMessage
	Calls []llm.ToolRequest
}

func New() *Client {
	return &Client{queue: map[string][]json.RawMessage{}}
}

// Enqueue appends resp (marshaled to JSON) as the next response returned
// for calls with the given tool name.
func (c *Client) Enqueue(toolName string, resp any) *Client {
	raw, err := json.Marshal(resp)
	if err != nil {
		panic(fmt.Sprintf("llmfake: marshal response for %s: %v", toolName, err))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue[toolName] = append(c.queue[toolName], raw)
	return c
}

func (c *Client) CallTool(ctx context.Context, req llm.ToolRequest) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Calls = append(c.Calls, req)

	q := c.queue[req.ToolName]
	if len(q) == 0 {
		return nil, fmt.Errorf("llmfake: no queued response for tool %q (call #%d)", req.ToolName, len(c.Calls))
	}
	c.queue[req.ToolName] = q[1:]
	return q[0], nil
}
