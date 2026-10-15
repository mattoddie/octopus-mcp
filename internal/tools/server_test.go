package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/octopus-mcp/internal/config"
)

func TestGatesControlWhichToolsExist(t *testing.T) {
	all := config.AllTools()
	registered := map[string]Info{}
	NewServer(nil, all, registered)

	readOnly := all
	readOnly.AllowWrites = false

	env := newEnv(t, readOnly)
	reads := toolNames(t, env.session)
	allTools := toolNames(t, connect(t, env.client, all))

	if len(allTools) != len(registered) {
		t.Errorf("registered %d, listed %d", len(registered), len(allTools))
	}
	writes := 0
	for name, info := range registered {
		_, inRead := reads[name]
		tool := allTools[name]
		if !strings.HasPrefix(name, "octopus_") {
			t.Errorf("%s: tool names start with octopus_", name)
		}
		switch info.Gate {
		case Read:
			if !inRead {
				t.Errorf("%s: read tool missing in read-only mode", name)
			}
			if !tool.Annotations.ReadOnlyHint && name != "octopus_graphql" {
				t.Errorf("%s: read tool without readOnlyHint", name)
			}
		case Write:
			writes++
			if inRead {
				t.Errorf("%s: write tool registered without OCTOPUS_ALLOW_WRITES", name)
			}
			if tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil {
				t.Errorf("%s: write tool must not be read-only and must say whether it is destructive", name)
			}
		}
	}
	if writes != 4 {
		t.Errorf("write tools: %d", writes)
	}
	// With writes off, the raw GraphQL tool is read-only; with writes on it may change things.
	if !reads["octopus_graphql"].Annotations.ReadOnlyHint || allTools["octopus_graphql"].Annotations.ReadOnlyHint {
		t.Error("octopus_graphql annotations should follow OCTOPUS_ALLOW_WRITES")
	}
	if tool := allTools["octopus_bump_charge"]; tool == nil || tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
		t.Error("a bump charge starts a real car charging and should be open-world")
	}
	if len(reads) < 18 {
		t.Errorf("unexpectedly few read tools: %d", len(reads))
	}
}

func TestToolsetsFilter(t *testing.T) {
	o := config.AllTools()
	o.Toolsets = map[string]bool{"tariffs": true}
	env := newEnv(t, o)
	names := toolNames(t, env.session)
	if len(names) != 6 {
		t.Errorf("tariffs toolset: %d tools", len(names))
	}
	for name := range names {
		if strings.Contains(name, "consumption") || strings.Contains(name, "account") {
			t.Errorf("%s registered with only the tariffs toolset", name)
		}
	}
}

func TestSchemasDescribeEveryArgument(t *testing.T) {
	env := newEnv(t)
	for name, tool := range toolNames(t, env.session) {
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s: missing title or description", name)
		}
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for arg, p := range props {
			if d, _ := p.(map[string]any)["description"].(string); d == "" {
				t.Errorf("%s.%s has no description", name, arg)
			}
		}
	}
}

func TestPrompts(t *testing.T) {
	env := newEnv(t)
	var names []string
	for p, err := range env.session.Prompts(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, p.Name)
	}
	if len(names) != len(prompts) {
		t.Errorf("prompts: %v", names)
	}
	res, err := env.session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "plan_cheap_usage", Arguments: map[string]string{"job": "dishwasher, 2 hours, 1.5 kWh"}})
	if err != nil {
		t.Fatal(err)
	}
	txt := res.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(txt, "dishwasher") || !strings.Contains(txt, "octopus_find_cheapest_slots") {
		t.Errorf("prompt text: %s", txt)
	}
	// Every tool a prompt mentions must exist.
	tools := toolNames(t, env.session)
	for _, p := range prompts {
		for _, word := range strings.FieldsFunc(p.text(map[string]string{}), func(r rune) bool { return !(r == '_' || r >= 'a' && r <= 'z') }) {
			if strings.HasPrefix(word, "octopus_") && tools[word] == nil {
				t.Errorf("prompt %s mentions unknown tool %s", p.name, word)
			}
		}
	}
}

func TestToolErrorsAreToolResults(t *testing.T) {
	env := newEnv(t)
	msg := env.callErr("octopus_get_product", map[string]any{"product_code": "NOPE-1"})
	if !strings.Contains(msg, "HTTP 404") || !strings.Contains(msg, "octopus_list_products") {
		t.Errorf("error: %s", msg)
	}
	// Schema validation rejects bad input before the handler runs.
	res, err := env.session.CallTool(context.Background(), &mcp.CallToolParams{Name: "octopus_get_consumption", Arguments: map[string]any{"group_by": "fortnight"}})
	if err == nil && !res.IsError {
		t.Error("invalid arguments were accepted")
	}
}

func TestInstructionsFollowWrites(t *testing.T) {
	if s := instructions(config.Tools{}); !strings.Contains(s, "read-only") {
		t.Errorf("read-only instructions: %s", s)
	}
	if s := instructions(config.Tools{AllowWrites: true}); !strings.Contains(s, "confirm with the user") {
		t.Errorf("write instructions: %s", s)
	}
}
