// Package tools defines octopus-mcp's MCP tools and prompts.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

// Obj is a JSON object from the API.
type Obj = octopus.Obj

// Gate is the setting a tool needs before it is registered.
type Gate int

const (
	Read  Gate = iota // always registered
	Write             // OCTOPUS_ALLOW_WRITES
)

func (g Gate) String() string {
	return [...]string{"read", "write"}[g]
}

// Context is shared by the tool definitions.
type Context struct {
	Server *mcp.Server
	Client *octopus.Client
	config.Tools
	// Registered records each registered tool's toolset and gate, for the docs generator.
	Registered map[string]Info
	// audit is the audit log, or nil when OCTOPUS_AUDIT_LOG is unset.
	audit *auditLog
}

// Info describes a registered tool.
type Info struct {
	Toolset string
	Gate    Gate
}

// Def describes a tool.
type Def struct {
	Toolset     string
	Name        string
	Title       string
	Description string
	Gate        Gate
	// ReadOnly overrides the readOnlyHint annotation (defaults to Gate == Read).
	ReadOnly *bool
	// Destructive overrides the destructiveHint annotation (defaults to false).
	Destructive *bool
	Idempotent  bool
	// OpenWorld marks tools whose effects reach beyond the account, such as a car starting to charge.
	OpenWorld bool
	// Enums restricts string arguments to fixed values, keyed by argument name.
	Enums map[string][]string
	// Audit decides whether a call goes in the audit log (OCTOPUS_AUDIT_LOG), for a
	// tool whose arguments decide whether it changes anything. When nil, every call
	// of a Write tool is logged and calls of Read tools are not.
	Audit func(args any) bool
}

func boolPtr(b bool) *bool { return &b }

// schemaFor infers the input schema from In: descriptions come from `jsonschema` tags, optional
// fields have `omitempty`, and Enums become enum lists.
func schemaFor[In any](d Def) *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: %v", d.Name, err))
	}
	var walk func(*jsonschema.Schema)
	walk = func(s *jsonschema.Schema) {
		if s == nil {
			return
		}
		// Optional pointer and slice fields infer as ["null", T]; plain T reads better, and omitting the argument is how to leave it out.
		if len(s.Types) == 2 && contains(s.Types, "null") {
			for _, t := range s.Types {
				if t != "null" {
					s.Type, s.Types = t, nil
				}
			}
		}
		for name, p := range s.Properties {
			if values, ok := d.Enums[name]; ok {
				target := p
				if p.Items != nil {
					target = p.Items
				}
				target.Enum = nil
				for _, v := range values {
					target.Enum = append(target.Enum, v)
				}
			}
			walk(p)
			walk(p.Items)
		}
	}
	walk(s)
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// textResult renders a handler's value as indented JSON.
func textResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b = []byte(fmt.Sprintf("%v", v))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// add registers a tool if its toolset and gate are enabled.
func add[In any](c *Context, d Def, h func(ctx context.Context, in In) (any, error)) {
	if !c.Toolsets[d.Toolset] {
		return
	}
	if d.Gate == Write && !c.AllowWrites {
		return
	}
	readOnly := d.Gate == Read
	if d.ReadOnly != nil {
		readOnly = *d.ReadOnly
	}
	ann := &mcp.ToolAnnotations{Title: d.Title, ReadOnlyHint: readOnly, IdempotentHint: d.Idempotent, OpenWorldHint: boolPtr(d.OpenWorld)}
	if !readOnly {
		ann.DestructiveHint = boolPtr(false)
		if d.Destructive != nil {
			ann.DestructiveHint = d.Destructive
		}
	}
	tool := &mcp.Tool{Name: d.Name, Title: d.Title, Description: d.Description, Annotations: ann, InputSchema: schemaFor[In](d)}
	logged := d.Audit
	if logged == nil && d.Gate == Write {
		logged = func(any) bool { return true }
	}
	mcp.AddTool(c.Server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := h(ctx, in)
		if c.audit != nil && logged != nil && logged(in) {
			c.audit.record(d.Name, in, err)
		}
		if err != nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}}, IsError: true}, nil, nil
		}
		return textResult(out), nil, nil
	})
	if c.Registered != nil {
		c.Registered[d.Name] = Info{Toolset: d.Toolset, Gate: d.Gate}
	}
}

// NoArgs is the input of tools without arguments.
type NoArgs struct{}
