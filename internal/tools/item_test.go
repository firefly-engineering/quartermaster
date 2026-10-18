package tools

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An explicit null prompt drops a work item's prompt and its ▶ buttons (#59).

const (
	// sessionStorePara is the fixture's session-store paragraph, from its prose to its prompt.
	sessionStorePara = "Unblocks #15. Cost: 10 min. Route: `/grill-with-docs`.\n" +
		"[▶ grill in web](obsidian://shell-commands/?vault=worklog&execute=abc&_date=2026-10-16&_item=session-store) ^session-store\n\n" +
		"> [!prompt]- Prompt sent to web\n> ```herdr-prompt id=session-store to=web\n" +
		"> /grill-with-docs https://github.com/acme/web/issues/14\n> ```\n"
	// sessionStoreNext is a Next line for session-store, with its button.
	sessionStoreNext = "4. Settle the session store · [[#^session-store|details]]" +
		" · [▶ grill in web](obsidian://shell-commands/?vault=worklog&execute=abc&_date=2026-10-16&_item=session-store)\n"
	vendorNext = "3. [ ] Call the vendor · 10 min\n"
)

// upsertInput decodes item_upsert's input from JSON, as the CLI and the MCP server do.
func upsertInput(t *testing.T, s string) ItemUpsertInput {
	t.Helper()
	var in ItemUpsertInput
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestItemUpsertInputPrompt(t *testing.T) {
	t.Parallel()
	for s, drop := range map[string]bool{
		`{"id": "a"}`:                 false,
		`{"id": "a", "prompt": null}`: true,
		`{"id": "a", "prompt": {"to": "w", "text": "x"}}`: false,
	} {
		if in := upsertInput(t, s); in.null.prompt != drop || in.ID != "a" {
			t.Errorf("%s: %+v, want a null prompt %v", s, in, drop)
		}
	}
	// Its own decoding still refuses unknown fields.
	var in ItemUpsertInput
	if err := json.Unmarshal([]byte(`{"id": "a", "promt": null}`), &in); err == nil {
		t.Error("an unknown field is accepted")
	}
}

func TestItemUpsertDropPrompt(t *testing.T) {
	t.Parallel()
	withNext := func(src string) string { return strings.Replace(src, vendorNext, vendorNext+sessionStoreNext, 1) }
	t.Run("a ##### item with a body stays one, without its button and prompt", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.itemUpsert(t.Context(), upsertInput(t, `{"id": "session-store", "title": "which session store?",
			"ref": "#14", "body": "Unblocks #15.", "cost": "10 min", "route": "/grill-with-docs", "prompt": null}`))
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, sessionStorePara, "Unblocks #15. Cost: 10 min. Route: `/grill-with-docs`. ^session-store\n"))
		if out.Item.Prompt != nil || out.Bucket != "Decisions for you" {
			t.Errorf("out %+v", out)
		}
	})
	t.Run("a ##### item without a body becomes a list item, keeping its route", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		if _, err := d.itemUpsert(t.Context(), upsertInput(t, `{"id": "session-store", "title": "which session store?",
			"ref": "#14", "cost": "10 min", "prompt": null}`)); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t,
			"##### ⬜ [#14](https://github.com/acme/web/issues/14): which session store?\n\n"+sessionStorePara+"\n", "",
			"- —\n\n"+yourActions, "- —\n- ⬜ [#14](https://github.com/acme/web/issues/14): which session store? · 10 min · Route: `/grill-with-docs` ^session-store\n\n"+yourActions))
	})
	t.Run("its Next line loses its button", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, withNext(settled(t)), false)
		if _, err := d.itemUpsert(t.Context(), upsertInput(t, `{"id": "session-store", "title": "which session store?",
			"ref": "#14", "body": "Unblocks #15.", "cost": "10 min", "route": "/grill-with-docs", "prompt": null}`)); err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(edited(t, sessionStorePara, "Unblocks #15. Cost: 10 min. Route: `/grill-with-docs`. ^session-store\n"),
			vendorNext, vendorNext+"4. Settle the session store · [[#^session-store|details]]\n", 1)
		d.checkNote(t, want)
	})
	t.Run("a button with a null prompt is refused", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		_, err := d.itemUpsert(t.Context(), upsertInput(t, `{"id": "session-store", "title": "x", "body": "y",
			"button": "go", "prompt": null}`))
		if err == nil || !strings.Contains(err.Error(), "prompt") {
			t.Errorf("err %v", err)
		}
		d.checkNote(t, settled(t))
	})
	t.Run("an absent prompt is still refused", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, withNext(settled(t)), false)
		for _, s := range []string{`{"id": "session-store", "title": "x"}`, `{"id": "session-store", "title": "x", "body": "y"}`} {
			if _, err := d.itemUpsert(t.Context(), upsertInput(t, s)); err == nil {
				t.Errorf("%s: no error", s)
			}
		}
		d.checkNote(t, withNext(settled(t)))
	})
}

// TestItemUpsertNullPromptMCP: the MCP schema takes a null prompt, and the server hands it on.
func TestItemUpsertNullPromptMCP(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	Register(s, All(d))
	cs := connect(t, s)

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(tools.Tools, func(tl *mcp.Tool) bool { return tl.Name == "item_upsert" })
	var schema struct {
		Properties map[string]struct {
			Types       []string `json:"type"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := remarshal(map[string]any{"properties": map[string]any{
		"prompt": tools.Tools[i].InputSchema.(map[string]any)["properties"].(map[string]any)["prompt"],
	}}, &schema); err != nil {
		t.Fatal(err)
	}
	if p := schema.Properties["prompt"]; !slices.Equal(p.Types, []string{"null", "object"}) || !strings.Contains(p.Description, "null") {
		t.Errorf("prompt schema %+v", p)
	}

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "item_upsert", Arguments: map[string]any{
		"id": "session-store", "title": "which session store?", "ref": "#14", "body": "Unblocks #15.",
		"cost": "10 min", "route": "/grill-with-docs", "prompt": nil,
	}})
	if err != nil || res.IsError {
		t.Fatalf("item_upsert: %v %+v", err, res)
	}
	d.checkNote(t, edited(t, sessionStorePara, "Unblocks #15. Cost: 10 min. Route: `/grill-with-docs`. ^session-store\n"))
}

// On update an absent ref, cost or route stays, "" counts as absent, and an explicit null
// clears one; on create a null counts as absent (#79).

// ie11Line is the fixture's one-line decision; ie11Item is it with an id, a cost and a route.
const (
	ie11Line = "- ⬜ [#16](https://github.com/acme/web/issues/16): drop IE11?\n"
	ie11Item = "- ⬜ [#16](https://github.com/acme/web/issues/16): drop IE11? · 5 min · Route: `/grill-with-docs` ^ie11\n"
)

func TestItemUpsertKeepsFields(t *testing.T) {
	t.Parallel()
	const link = "[#16](https://github.com/acme/web/issues/16): "
	for in, want := range map[string]string{
		`{"id": "ie11", "title": "drop IE11 now?"}`:                "- ⬜ " + link + "drop IE11 now? · 5 min · Route: `/grill-with-docs` ^ie11\n",
		`{"id": "ie11", "title": "drop IE11 now?", "ref": ""}`:     "- ⬜ " + link + "drop IE11 now? · 5 min · Route: `/grill-with-docs` ^ie11\n",
		`{"id": "ie11", "title": "drop IE11 now?", "ref": null}`:   "- ⬜ drop IE11 now? · 5 min · Route: `/grill-with-docs` ^ie11\n",
		`{"id": "ie11", "title": "drop IE11 now?", "cost": null}`:  "- ⬜ " + link + "drop IE11 now? · Route: `/grill-with-docs` ^ie11\n",
		`{"id": "ie11", "title": "drop IE11 now?", "route": null}`: "- ⬜ " + link + "drop IE11 now? · 5 min ^ie11\n",
		`{"id": "ie11", "title": "drop IE11 now?", "ref": "#18", "cost": "1 h", "route": "/triage"}`: "- ⬜ " +
			"[#18](https://github.com/acme/web/issues/18): drop IE11 now? · 1 h · Route: `/triage` ^ie11\n",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, edited(t, ie11Line, ie11Item), false)
			if _, err := d.itemUpsert(t.Context(), upsertInput(t, in)); err != nil {
				t.Fatal(err)
			}
			d.checkNote(t, edited(t, ie11Line, want))
		})
	}
	t.Run("a ##### item keeps its ref, cost and route", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.itemUpsert(t.Context(), upsertInput(t, `{"id": "session-store", "title": "which session store?",
			"body": "Unblocks #15.", "prompt": null}`))
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, sessionStorePara, "Unblocks #15. Cost: 10 min. Route: `/grill-with-docs`. ^session-store\n"))
		if out.Item.Title != "[#14](https://github.com/acme/web/issues/14): which session store?" {
			t.Errorf("title %q", out.Item.Title)
		}
	})
	t.Run("a create takes a null as absent", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		if _, err := d.itemUpsert(t.Context(), upsertInput(t, `{"id": "flaky", "repo": "acme/web", "bucket": "Agent queue",
			"title": "flaky test", "ref": null, "cost": null, "route": null}`)); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, wave2, wave2+"- ⬜ flaky test ^flaky\n"))
	})
}

// TestItemUpsertNullFieldsMCP: the MCP schema takes a null ref, cost and route, and the server
// hands it on.
func TestItemUpsertNullFieldsMCP(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, edited(t, ie11Line, ie11Item), false)
	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	Register(s, All(d))
	cs := connect(t, s)

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(tools.Tools, func(tl *mcp.Tool) bool { return tl.Name == "item_upsert" })
	var schema struct {
		Properties map[string]struct {
			Types       []string `json:"type"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	props := tools.Tools[i].InputSchema.(map[string]any)["properties"].(map[string]any)
	if err := remarshal(map[string]any{"properties": map[string]any{
		"ref": props["ref"], "cost": props["cost"], "route": props["route"],
	}}, &schema); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ref", "cost", "route"} {
		if p := schema.Properties[f]; !slices.Equal(p.Types, []string{"null", "string"}) || !strings.Contains(p.Description, "null clears it") {
			t.Errorf("%s schema %+v", f, p)
		}
	}

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "item_upsert", Arguments: map[string]any{
		"id": "ie11", "title": "drop IE11?", "ref": nil, "route": nil,
	}})
	if err != nil || res.IsError {
		t.Fatalf("item_upsert: %v %+v", err, res)
	}
	d.checkNote(t, edited(t, ie11Line, "- ⬜ drop IE11? · 5 min ^ie11\n"))
}
