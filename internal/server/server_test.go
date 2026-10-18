package server_test

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/firefly-engineering/quartermaster/internal/server"
	"github.com/firefly-engineering/quartermaster/internal/tools"
	"github.com/firefly-engineering/quartermaster/skills"
)

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	srv, err := server.New("test")
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func texts(t *testing.T, res *mcp.GetPromptResult) []string {
	t.Helper()
	var out []string
	for _, m := range res.Messages {
		tc, ok := m.Content.(*mcp.TextContent)
		if !ok {
			t.Fatalf("content %T, want text", m.Content)
		}
		out = append(out, tc.Text)
	}
	return out
}

func embedded(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(skills.Worklog, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestListPrompts(t *testing.T) {
	t.Parallel()
	cs := connect(t)
	res, err := cs.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range res.Prompts {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"phase", "workflow_rules"}) {
		t.Fatalf("prompts %v", names)
	}
}

func TestListTools(t *testing.T) {
	t.Parallel()
	srv, err := server.New("test", tools.All(tools.Deps{})...)
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	if !slices.Equal(names, []string{"carryover_verdict", "day_open", "day_read", "item_transition", "item_upsert",
		"next_set", "objective_set", "refresh", "status_set", "task_dispatch"}) {
		t.Fatalf("tools %v", names)
	}
}

func TestWorkflowRules(t *testing.T) {
	t.Parallel()
	cs := connect(t)
	res, err := cs.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "workflow_rules"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{embedded(t, "worklog/SKILL.md"), embedded(t, "worklog/template.md")}
	if got := texts(t, res); !slices.Equal(got, want) {
		t.Fatalf("got %d messages, want SKILL.md then template.md", len(got))
	}
}

func TestPhase(t *testing.T) {
	t.Parallel()
	skill := embedded(t, "worklog/SKILL.md")
	titles := map[string]string{
		"open": "Open", "unblock": "Unblock", "afk": "AFK",
		"land": "Land", "capture": "Capture", "close": "Close",
	}
	for name, title := range titles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cs := connect(t)
			res, err := cs.GetPrompt(t.Context(), &mcp.GetPromptParams{
				Name: "phase", Arguments: map[string]string{"name": name},
			})
			if err != nil {
				t.Fatal(err)
			}
			got := texts(t, res)
			if len(got) != 1 {
				t.Fatalf("%d messages, want 1", len(got))
			}
			text := got[0]
			if !strings.HasPrefix(text, "### "+title+"\n") || !strings.Contains(skill, text) {
				t.Fatalf("not the %s section: %q", title, text)
			}
			if strings.Contains(text[1:], "\n## ") || strings.Contains(text[1:], "\n### ") {
				t.Fatalf("section runs past its end: %q", text)
			}
		})
	}
}

func TestPhaseUnknown(t *testing.T) {
	t.Parallel()
	cs := connect(t)
	_, err := cs.GetPrompt(t.Context(), &mcp.GetPromptParams{
		Name: "phase", Arguments: map[string]string{"name": "lunch"},
	})
	if err == nil {
		t.Fatal("want an error for an unknown phase")
	}
}
