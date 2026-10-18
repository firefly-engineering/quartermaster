package note

import (
	"strconv"
	"strings"
	"testing"
)

var testRenderer = Renderer{Vault: "worklog", Date: "2026-10-18", CommandID: "nf8lgvkx5s"}

// templateDay is the structured input whose rendering is the filled-in template.md.
func templateDay() Day {
	return Day{
		Date:     "2026-10-18",
		Previous: "2026-10-17",
		Status:   StatusUnit{Time: "09:30", Text: "opened."},
		Objectives: []ObjectiveUnit{
			{Objective: "Ship the parser (quartermaster)", DoneWhen: "#2 closed", Icon: IconTodo, Progress: "0/1"},
		},
		Next: []NextUnit{
			{Action: "Settle #2", Cost: "20 min", Ref: "grill-2", Button: "grill in quartermaster"},
			{Action: "Rotate the token", Cost: "20 min", Ref: "grill-2"},
		},
		Projects: []ProjectUnit{{
			Icon: IconTodo,
			Repo: "firefly-engineering/quartermaster",
			Parts: []PartUnit{
				{Icon: IconTodo, N: 1, Kind: "Human session", Title: "spec, done", Body: "- ✅ decided."},
			},
			Remains: IconTodo,
			Buckets: []BucketUnit{
				{Icon: IconTodo, Name: "Decisions for you", Items: []ItemUnit{
					{
						ID: "grill-2", Icon: IconTodo, Heading: true,
						Title:  "[#N](url): is the parser strict?",
						Body:   "Unblocks #3. 20 min. Route: `/grill-with-docs`, it touches ADR 0002.",
						Button: "grill in quartermaster",
						Prompt: &PromptUnit{To: "quartermaster", Body: "/grill-with-docs https://github.com/firefly-engineering/quartermaster/issues/2"},
					},
					{Icon: IconTodo, Title: "[#M](url): a decision with no prompt yet"},
				}},
				{Icon: IconTodo, Name: "Your actions"},
				{Icon: IconTodo, Name: "Agent queue", Items: []ItemUnit{
					{Icon: IconTodo, Title: "[#N](url) spec, after [#M](url). A batch ready to start is a `#####` work item with its button and prompt"},
				}},
				{Icon: IconTodo, Name: "Sent to other repos", Items: []ItemUnit{
					{Icon: IconTodo, Title: "a grant → quartermaster, **sent**, [IaC#1](https://example.com/1). While **to send**, it is a `#####` work item with its button and prompt"},
				}},
				{Icon: IconTodo, Name: "Outside input: chase"},
				{Icon: IconTodo, Name: "Waiting"},
			},
		}},
		CarryOver: []CarryOverUnit{{Item: "[#9](https://example.com/9)", State: "open", Verdict: "defer: tomorrow"}},
	}
}

// TestGoldenTemplate: the renderer and template.md are kept in step (ADR 0002).
func TestGoldenTemplate(t *testing.T) {
	t.Parallel()
	r := testRenderer
	r.Date = "2000-01-01" // the note's own date wins for its buttons
	got, err := r.Note(templateDay())
	if err != nil {
		t.Fatal(err)
	}
	if want := string(filledTemplate(t)); string(got) != want {
		t.Errorf("rendered note differs from template.md\n%s", lineDiff(want, string(got)))
	}
}

// lineDiff names the first line where want and got part.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return "line " + strconv.Itoa(i+1) + ":\nwant " + strconv.Quote(wl) + "\ngot  " + strconv.Quote(gl)
		}
	}
	return "(equal lines)"
}

// TestNoButtonWithoutCommandID: with no Shell commands id the buttons go, the prompt stays.
func TestNoButtonWithoutCommandID(t *testing.T) {
	t.Parallel()
	r := testRenderer
	r.CommandID = ""
	got, err := r.Note(templateDay())
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "obsidian://") || strings.Contains(s, "▶") {
		t.Errorf("a button was rendered:\n%s", s)
	}
	if !strings.Contains(s, "Route: `/grill-with-docs`, it touches ADR 0002. ^grill-2\n\n> [!prompt]- Prompt sent to quartermaster\n") {
		t.Errorf("the item lost its id or prompt:\n%s", s)
	}
	n, err := Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if it := n.Item("grill-2"); it == nil || it.Prompt == nil || it.Prompt.To != "quartermaster" {
		t.Errorf("item: %+v", it)
	}
}

func TestRenderUnits(t *testing.T) {
	t.Parallel()
	r := testRenderer
	cases := []struct {
		name string
		got  func() ([]byte, error)
		want string
	}{
		{"status", func() ([]byte, error) { return r.Status(StatusUnit{Time: "12:21", Text: "wave 1 running."}) },
			"**12:21 UTC:** wave 1 running.\n"},
		{"status with label", func() ([]byte, error) {
			return r.Status(StatusUnit{Time: "12:21", Label: "unblocked", Text: "wave 1 running."})
		}, "**12:21 UTC, unblocked:** wave 1 running.\n"},
		{"objective", func() ([]byte, error) {
			return r.Objective(ObjectiveUnit{Objective: "Ship (qm)", DoneWhen: "#2 closed", Icon: IconInProgress, Progress: "1/2"})
		}, "| Ship (qm) | #2 closed | 🔄 1/2 |\n"},
		{"objective no progress, pipe escaped", func() ([]byte, error) {
			return r.Objective(ObjectiveUnit{Objective: "[[a|b]]", DoneWhen: "x \\| y", Icon: IconDone})
		}, "| [[a\\|b]] | x \\| y | ✅ |\n"},
		{"double backslash then pipe", func() ([]byte, error) {
			return r.CarryOver(CarryOverUnit{Item: `a\\|b`, State: `c\|d`, Verdict: "e"})
		}, "| a\\\\\\|b | c\\|d | e |\n"},
		{"vault with a space", func() ([]byte, error) {
			r := r
			r.Vault = "my vault"
			return r.NextLine(1, NextUnit{Action: "x", Ref: "a", Button: "go"})
		}, "1. x · [[#^a|details]] · [▶ go](obsidian://shell-commands/?vault=my%20vault&execute=nf8lgvkx5s&_date=2026-10-18&_item=a)\n"},
		{"carry-over", func() ([]byte, error) {
			return r.CarryOver(CarryOverUnit{Item: "[#9](u)", State: "open", Verdict: "keep"})
		}, "| [#9](u) | open | keep |\n"},
		{"next with button", func() ([]byte, error) {
			return r.NextLine(3, NextUnit{Action: "Settle #819", Cost: "20 min", Ref: "grill-819", Button: "grill in trunk"})
		}, "3. Settle #819 · 20 min · [[#^grill-819|details]] · [▶ grill in trunk](obsidian://shell-commands/?vault=worklog&execute=nf8lgvkx5s&_date=2026-10-18&_item=grill-819)\n"},
		{"next manual action", func() ([]byte, error) {
			return r.NextLine(1, NextUnit{Box: Unchecked, Action: "Rotate the token", Cost: "5 min", Ref: "rotate"})
		}, "1. [ ] Rotate the token · 5 min · [[#^rotate|details]]\n"},
		{"next ticked", func() ([]byte, error) {
			return r.NextLine(2, NextUnit{Box: Checked, Action: "Tick the box"})
		}, "2. [x] Tick the box\n"},
		{"list item", func() ([]byte, error) {
			return r.Item(ItemUnit{ID: "impl-4", Icon: IconInReview, Title: "[#4](u) in review"})
		}, "- 🔍 [#4](u) in review ^impl-4\n"},
		{"list entry without id", func() ([]byte, error) {
			return r.Item(ItemUnit{Icon: IconTodo, Title: "[#5](u) later"})
		}, "- ⬜ [#5](u) later\n"},
		{"heading item without prompt", func() ([]byte, error) {
			return r.Item(ItemUnit{ID: "send-iac", Icon: IconWaiting, Heading: true, Title: "Grant → IaC", Body: "To send once #3 lands."})
		}, "##### ⏳ Grant → IaC\n\nTo send once #3 lands. ^send-iac\n"},
		{"heading item with prompt", func() ([]byte, error) {
			return r.Item(ItemUnit{
				ID: "grill-819", Icon: IconTodo, Heading: true, Title: "[#819](u): admissible?",
				Body: "Every Run is refused.\n~20 min.", Button: "grill in trunk",
				Prompt: &PromptUnit{To: "trunk", Body: "/grill-with-docs u\n\nthe question\n"},
			})
		}, "##### ⬜ [#819](u): admissible?\n\nEvery Run is refused.\n~20 min.\n" +
			"[▶ grill in trunk](obsidian://shell-commands/?vault=worklog&execute=nf8lgvkx5s&_date=2026-10-18&_item=grill-819) ^grill-819\n\n" +
			"> [!prompt]- Prompt sent to trunk\n> ```herdr-prompt id=grill-819 to=trunk\n> /grill-with-docs u\n>\n> the question\n> ```\n"},
		{"heading item, id alone", func() ([]byte, error) {
			return r.Item(ItemUnit{ID: "x", Icon: IconTodo, Heading: true, Title: "X"})
		}, "##### ⬜ X\n\n^x\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := c.got()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("got\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

// TestRenderedItemParses: a rendered work item reads back as the unit it was rendered from.
func TestRenderedItemParses(t *testing.T) {
	t.Parallel()
	for _, u := range []ItemUnit{
		{ID: "a", Icon: IconTodo, Heading: true, Title: "A", Body: "b.", Button: "go in t", Prompt: &PromptUnit{To: "t", Body: "line\n\n  indented\n"}},
		{ID: "a", Icon: IconTodo, Heading: true, Title: "A", Prompt: &PromptUnit{To: "t", Body: "x"}},
		{ID: "a", Icon: IconDone, Title: "[#1](u) done"},
	} {
		b, err := testRenderer.Item(u)
		if err != nil {
			t.Fatal(err)
		}
		src := "# Worklog — 2026-10-18\n\n**09:00 UTC:** x.\n\n## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n" + string(b)
		n, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("%v\n%s", err, src)
		}
		it := n.Item("a")
		if it == nil || it.Icon != u.Icon || it.Heading != u.Heading || it.Title != u.Title {
			t.Fatalf("item %+v from\n%s", it, b)
		}
		if u.Prompt != nil {
			want := u.Prompt.Body
			if !strings.HasSuffix(want, "\n") {
				want += "\n"
			}
			if it.Prompt == nil || it.Prompt.To != u.Prompt.To || it.Prompt.Body != want {
				t.Errorf("prompt %+v, want body %q", it.Prompt, want)
			}
		}
	}
}

func TestRenderErrors(t *testing.T) {
	t.Parallel()
	r := testRenderer
	cases := []struct {
		name string
		err  func() error
		msg  string
	}{
		{"bad time", func() error { _, err := r.Status(StatusUnit{Time: "9:30", Text: "x"}); return err }, "HH:MM"},
		{"status newline", func() error { _, err := r.Status(StatusUnit{Time: "09:30", Text: "a\nb"}); return err }, "one line"},
		{"bad item id", func() error { _, err := r.Item(ItemUnit{ID: "Bad", Icon: IconTodo, Title: "x"}); return err }, "item id"},
		{"item heading icon", func() error { _, err := r.Item(ItemUnit{ID: "a", Icon: IconNone, Title: "x"}); return err }, "icon"},
		{"heading without id", func() error { _, err := r.Item(ItemUnit{Icon: IconTodo, Heading: true, Title: "x"}); return err }, "id"},
		{"prompt on list item", func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Title: "x", Prompt: &PromptUnit{To: "t", Body: "p"}})
			return err
		}, "#####"},
		{"button without prompt", func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Heading: true, Title: "x", Button: "go"})
			return err
		}, "prompt"},
		{"blank line in body", func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Heading: true, Title: "x", Body: "a\n\nb"})
			return err
		}, "blank"},
		{"fence in prompt", func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Heading: true, Title: "x", Prompt: &PromptUnit{To: "t", Body: "```\n"}})
			return err
		}, "```"},
		{"prompt to with space", func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Heading: true, Title: "x", Prompt: &PromptUnit{To: "a b", Body: "p"}})
			return err
		}, "to="},
		{"status label colon", func() error { _, err := r.Status(StatusUnit{Time: "09:30", Label: "a:b", Text: "x"}); return err }, "label"},
		{"title ending in id", func() error { _, err := r.Item(ItemUnit{Icon: IconTodo, Title: "x ^y"}); return err }, "block id"},
		{"manual action with button", func() error {
			_, err := r.NextLine(1, NextUnit{Box: Unchecked, Action: "x", Ref: "a", Button: "go"})
			return err
		}, "manual action"},
		{"next bad ref", func() error { _, err := r.NextLine(1, NextUnit{Action: "x", Ref: "Bad"}); return err }, "item id"},
		{"next button without ref", func() error { _, err := r.NextLine(1, NextUnit{Action: "x", Button: "go"}); return err }, "Ref"},
		{"next number", func() error { _, err := r.NextLine(0, NextUnit{Action: "x"}); return err }, "number"},
		{"objective icon", func() error { _, err := r.Objective(ObjectiveUnit{Objective: "x", Icon: "?"}); return err }, "icon"},
		{"cell newline", func() error { _, err := r.CarryOver(CarryOverUnit{Item: "a\nb"}); return err }, "one line"},
		{"bad date", func() error { d := templateDay(); d.Date = "18/10"; _, err := r.Note(d); return err }, "date"},
		{"bad repo", func() error { d := templateDay(); d.Projects[0].Repo = "x"; _, err := r.Note(d); return err }, "repo"},
		{"bad part kind", func() error { d := templateDay(); d.Projects[0].Parts[0].Kind = "Nap"; _, err := r.Note(d); return err }, "kind"},
		{"bad bucket", func() error {
			d := templateDay()
			d.Projects[0].Buckets[0].Name = "Someday"
			_, err := r.Note(d)
			return err
		}, "bucket"},
		{"bad button date", func() error {
			r := r
			r.Date = "x"
			_, err := r.NextLine(1, NextUnit{Action: "x", Ref: "a", Button: "go"})
			return err
		}, "date"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := c.err()
			if err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Errorf("got %v, want an error containing %q", err, c.msg)
			}
		})
	}
}

// TestMinimalDay: a day with nothing in it, and empty `—` headings, renders a note the parser reads.
func TestMinimalDay(t *testing.T) {
	t.Parallel()
	got, err := testRenderer.Note(Day{
		Date: "2026-10-18", Status: StatusUnit{Time: "08:00", Text: "opened."},
		Projects: []ProjectUnit{{Icon: IconNone, Repo: "o/r", Remains: IconNone, Buckets: []BucketUnit{{Icon: IconNone, Name: "Waiting"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(got)
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if n.Objectives == nil || n.Next == nil || n.CarryOver == nil || n.Process == nil || len(n.Projects) != 1 ||
		n.Projects[0].Icon != IconNone || n.Projects[0].Remains.Buckets[0].Icon != IconNone {
		t.Errorf("sections missing:\n%s", got)
	}
	if strings.Contains(string(got), "previous:") || strings.Contains(string(got), "From [[") {
		t.Errorf("no previous note, yet:\n%s", got)
	}
}
