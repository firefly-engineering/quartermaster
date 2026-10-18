// Package server is quartermaster's MCP server: the prompts of docs/spec.md §8, served from
// the worklog skill embedded in the binary (ADR 0005), and the tools of §6 from internal/tools.
package server

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/firefly-engineering/quartermaster/internal/tools"
	"github.com/firefly-engineering/quartermaster/skills"
)

// New returns the MCP server, its prompts read from the embedded skill, serving the tools ts.
// version is reported to clients as the server's version.
func New(version string, ts ...tools.Tool) (*mcp.Server, error) {
	skill, err := fs.ReadFile(skills.Worklog, "worklog/SKILL.md")
	if err != nil {
		return nil, err
	}
	template, err := fs.ReadFile(skills.Worklog, "worklog/template.md")
	if err != nil {
		return nil, err
	}
	phases, err := cutPhases(skill)
	if err != nil {
		return nil, fmt.Errorf("worklog/SKILL.md: %w", err)
	}
	if len(phases) == 0 {
		return nil, fmt.Errorf("worklog/SKILL.md: no phases under %q", cycleHeading)
	}
	names := slices.Sorted(maps.Keys(phases))

	s := mcp.NewServer(&mcp.Implementation{Name: "quartermaster", Version: version}, nil)
	s.AddPrompt(&mcp.Prompt{
		Name:        "workflow_rules",
		Description: "The worklog skill: SKILL.md, then template.md.",
	}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return userMessages(string(skill), string(template)), nil
	})
	s.AddPrompt(&mcp.Prompt{
		Name:        "phase",
		Description: "One phase's section of the worklog skill, for a harness that wants less context.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "name",
			Description: "The phase: " + strings.Join(names, ", ") + ".",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		name := req.Params.Arguments["name"]
		section, ok := phases[name]
		if !ok {
			return nil, fmt.Errorf("unknown phase %q, want one of: %s", name, strings.Join(names, ", "))
		}
		return userMessages(section), nil
	})
	tools.Register(s, ts)
	return s, nil
}

// userMessages is a prompt result of one user message per text, in order.
func userMessages(texts ...string) *mcp.GetPromptResult {
	msgs := make([]*mcp.PromptMessage, len(texts))
	for i, t := range texts {
		msgs[i] = &mcp.PromptMessage{Role: "user", Content: &mcp.TextContent{Text: t}}
	}
	return &mcp.GetPromptResult{Messages: msgs}
}
