package note

import (
	"errors"
	"strings"
	"testing"
)

// The formats the write tools need beyond day_open's: refs, an item's cost, route and summary,
// its title line alone, the refined stamp, the verdict cell, and sections on their own.

func TestParseRef(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Ref{
		"acme/web#12":    {Repo: "acme/web", N: 12},
		"#7":             {Repo: "o/r", N: 7},
		"Acme.co/my_r#1": {Repo: "Acme.co/my_r", N: 1},
	} {
		got, err := ParseRef(in, "o/r")
		if err != nil || got != want {
			t.Errorf("%s: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "#", "#0", "#-1", "#x", "acme#1", "acme/web", "acme/web#", "a/b/c#1", "acme/web#1 x"} {
		if got, err := ParseRef(in, "o/r"); err == nil {
			t.Errorf("%q: %+v, want an error", in, got)
		}
	}
	if _, err := ParseRef("#7", ""); err == nil || !strings.Contains(err.Error(), "owner/repo#N") {
		t.Errorf("a bare #N with no project in scope: %v", err)
	}
	if s := (Ref{Repo: "acme/web", N: 12}).String(); s != "acme/web#12" {
		t.Errorf("String %q", s)
	}
}

func TestRenderWriteUnits(t *testing.T) {
	t.Parallel()
	r := testRenderer
	web12 := Ref{Repo: "acme/web", N: 12}
	cases := []struct {
		name string
		got  func() ([]byte, error)
		want string
	}{
		{"list item with ref, cost, route and summary", func() ([]byte, error) {
			return r.Item(ItemUnit{
				ID: "login", Icon: IconInReview, Repo: "acme/web", Ref: web12, Title: "login form",
				Cost: "20 min", Route: "/implement", Summary: "PR up",
			})
		}, "- 🔍 [#12](https://github.com/acme/web/issues/12): login form · 20 min · Route: `/implement` — PR up ^login\n"},
		{"ref in another project", func() ([]byte, error) {
			return r.Item(ItemUnit{ID: "grant", Icon: IconTodo, Repo: "acme/web", Ref: Ref{Repo: "acme/IaC", N: 564}, Title: "grant"})
		}, "- ⬜ [IaC#564](https://github.com/acme/IaC/issues/564): grant ^grant\n"},
		{"heading item with cost, route and summary", func() ([]byte, error) {
			return r.Item(ItemUnit{
				ID: "store", Icon: IconInProgress, Heading: true, Repo: "acme/web", Ref: Ref{Repo: "acme/web", N: 14},
				Title: "which store?", Body: "Unblocks #15.", Cost: "10 min", Route: "/grill-with-docs", Summary: "grilling",
				Button: "grill in web", Prompt: &PromptUnit{To: "web", Body: "/grill-with-docs u"},
			})
		}, "##### 🔄 [#14](https://github.com/acme/web/issues/14): which store? — grilling\n\n" +
			"Unblocks #15. 10 min. Route: `/grill-with-docs`.\n" +
			"[▶ grill in web](obsidian://shell-commands/?vault=worklog&execute=nf8lgvkx5s&_date=2026-10-18&_item=store) ^store\n\n" +
			"> [!prompt]- Prompt sent to web\n> ```herdr-prompt id=store to=web\n> /grill-with-docs u\n> ```\n"},
		{"heading item, route alone", func() ([]byte, error) {
			return r.Item(ItemUnit{ID: "x", Icon: IconTodo, Heading: true, Title: "X", Route: "/triage"})
		}, "##### ⬜ X\n\nRoute: `/triage`. ^x\n"},
		{"list title line", func() ([]byte, error) {
			return r.ItemTitle(ItemUnit{ID: "login", Icon: IconDone, Title: "[#12](u): login form · 20 min", Summary: "merged in abc123"})
		}, "✅ [#12](u): login form · 20 min — merged in abc123 ^login\n"},
		{"heading title line", func() ([]byte, error) {
			return r.ItemTitle(ItemUnit{ID: "store", Icon: IconWaiting, Heading: true, Title: "which store?", Summary: "waits on — ops"})
		}, "⏳ which store? — waits on — ops\n"},
		{"refined objective", func() ([]byte, error) {
			return r.Objective(ObjectiveUnit{
				Objective: "Ship (acme/web)", DoneWhen: "#12 merged", Icon: IconTodo, Refined: "10:05", Reason: "scope cut",
			})
		}, "| Ship (acme/web) (refined 10:05 UTC: scope cut) | #12 merged | ⬜ |\n"},
		{"bucket", func() ([]byte, error) {
			return r.Bucket(BucketUnit{Icon: IconNone, Name: "Waiting"})
		}, "#### — Waiting\n\n- —\n"},
		{"remains", func() ([]byte, error) {
			return r.Remains(IconTodo, []BucketUnit{
				{Icon: IconTodo, Name: "Agent queue", Items: []ItemUnit{{ID: "a", Icon: IconTodo, Title: "A"}}},
				{Icon: IconNone, Name: "Waiting"},
			})
		}, "### ⬜ What remains\n\n#### ⬜ Agent queue\n\n- ⬜ A ^a\n\n#### — Waiting\n\n- —\n"},
		{"project", func() ([]byte, error) {
			return r.Project(ProjectUnit{Icon: IconTodo, Repo: "acme/web", Remains: IconTodo, Buckets: []BucketUnit{
				{Icon: IconTodo, Name: "Agent queue", Items: []ItemUnit{{ID: "a", Icon: IconTodo, Ref: web12, Title: "A"}}},
			}})
		}, "## ⬜ acme/web\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n- ⬜ [#12](https://github.com/acme/web/issues/12): A ^a\n"},
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

func TestRenderWriteErrors(t *testing.T) {
	t.Parallel()
	r := testRenderer
	for name, c := range map[string]struct {
		err func() error
		msg string
	}{
		"summary separator in title": {func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Title: "a — b"})
			return err
		}, SummarySep},
		"summary separator in cost": {func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Title: "a", Cost: "1 — 2 h"})
			return err
		}, SummarySep},
		"backtick in route": {func() error {
			_, err := r.Item(ItemUnit{ID: "a", Icon: IconTodo, Title: "a", Route: "`/x`"})
			return err
		}, "route"},
		"summary newline": {func() error {
			_, err := r.ItemTitle(ItemUnit{ID: "a", Icon: IconTodo, Title: "a", Summary: "x\ny"})
			return err
		}, "one line"},
		"refined without reason": {func() error {
			_, err := r.Objective(ObjectiveUnit{Objective: "x", Icon: IconTodo, Refined: "10:00"})
			return err
		}, "reason"},
		"refined bad time": {func() error {
			_, err := r.Objective(ObjectiveUnit{Objective: "x", Icon: IconTodo, Refined: "10h", Reason: "r"})
			return err
		}, "HH:MM"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := c.err(); err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Errorf("got %v, want an error containing %q", err, c.msg)
			}
		})
	}
}

// TestAction: a work item's title as its Next line's action leaves out its cost, route and summary.
func TestAction(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"[#9](u): flaky test · 20 min · Route: `/implement` — PR up": "[#9](u): flaky test",
		"which store? — grilling":                                    "which store?",
		"plain":                                                      "plain",
	} {
		if got := Action(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestSplitSummary(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][2]string{
		"login form":               {"login form", ""},
		"login form — PR up":       {"login form", "PR up"},
		"login form — PR — merged": {"login form", "PR — merged"},
		"a—b":                      {"a—b", ""},
	} {
		if h, s := SplitSummary(in); h != want[0] || s != want[1] {
			t.Errorf("%q: %q %q, want %q", in, h, s, want)
		}
	}
}

func TestStripRefined(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Ship (o/r)": "Ship (o/r)",
		"Ship (o/r) (refined 10:05 UTC: scope cut)": "Ship (o/r)",
		"Ship (refined soon)":                       "Ship (refined soon)",
		"Ship (refined 1:05 UTC: x)":                "Ship (refined 1:05 UTC: x)",
	} {
		if got := StripRefined(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestParseState(t *testing.T) {
	t.Parallel()
	for s := Todo; s <= Dropped; s++ {
		if got, err := ParseState(s.String()); got != s || err != nil {
			t.Errorf("%s: %v %v", s, got, err)
		}
	}
	for _, in := range []string{"", "done", "State(0)"} {
		if _, err := ParseState(in); err == nil || !strings.Contains(err.Error(), "TODO, IN_PROGRESS") {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestVerdictCell(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ verdict, reason, want string }{
		{"keep", "", "keep"},
		{"defer", "hardware", "defer: hardware"},
		{"drop", "closed overnight", "drop: closed overnight"},
	} {
		got, err := VerdictCell(c.verdict, c.reason)
		if err != nil || got != c.want {
			t.Errorf("%s %s: %q %v", c.verdict, c.reason, got, err)
		}
		if row := (CarryOverRow{Verdict: got}); row.Open() != (c.verdict == "defer") {
			t.Errorf("%q reads as open %v", got, row.Open())
		}
	}
	for _, c := range [][2]string{{"drop", ""}, {"maybe", "x"}, {"keep", "a\nb"}} {
		if _, err := VerdictCell(c[0], c[1]); err == nil {
			t.Errorf("%q: no error", c)
		}
	}
}

// TestTransitionItem moves each form of work item, and ticks a manual action, splicing only its
// head line.
func TestTransitionItem(t *testing.T) {
	t.Parallel()
	src := []byte(itemNote + "\n#### ⬜ Your actions\n\n- [ ] Rotate the token ^rotate\n")
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id      string
		to      State
		summary string
		from    string
		want    string
	}{
		{"store", InProgress, "grilling", "##### ⬜ [#14](https://github.com/o/r/issues/14): which store?\n",
			"##### 🔄 [#14](https://github.com/o/r/issues/14): which store? — grilling\n"},
		{"ie11", Dropped, "won't fix", "- ⬜ [#16](https://github.com/o/r/issues/16): drop IE11,\n  or keep it? ^ie11\n",
			"- ❌ [#16](https://github.com/o/r/issues/16): drop IE11, or keep it? — won't fix ^ie11\n"},
		// No summary keeps the one there.
		{"nested", InReview, "", "  - 🔄 nested — PR up ^nested\n", "  - 🔍 nested — PR up ^nested\n"},
		{"rotate", Done, "", "- [ ] Rotate the token ^rotate\n", "- [x] Rotate the token ^rotate\n"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			got, err := TransitionItem(n, src, c.id, c.to, c.summary)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(string(src), c.from, c.want, 1)
			if string(got) != want {
				t.Errorf("got\n%s", lineDiff(want, string(got)))
			}
			if _, err := Parse(got); err != nil {
				t.Errorf("result does not parse: %v", err)
			}
		})
	}
	for _, c := range []struct {
		id      string
		to      State
		summary string
		err     error
		msg     string
	}{
		{"store", InReview, "", ErrIllegalTransition, ""},
		{"store", Waiting, " ", ErrReasonRequired, ""},
		{"nested", Done, "", ErrReasonRequired, ""}, // IN_PROGRESS to DONE needs its evidence
		{"rotate", Dropped, "moot", nil, "only be ticked"},
		{"rotate", Done, "done", nil, "summary"},
		{"nope", Done, "", ErrNoItem, ""},
	} {
		_, err := TransitionItem(n, src, c.id, c.to, c.summary)
		if err == nil || c.err != nil && !errors.Is(err, c.err) || c.msg != "" && !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s to %s: %v, want %v %q", c.id, c.to, err, c.err, c.msg)
		}
	}
}
