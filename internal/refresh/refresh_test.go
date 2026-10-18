package refresh

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// fake stands in for gh or herdr: each call's key, from keyOf, names its reply. A call with no
// reply fails the test, so a test also proves what refresh does not ask.
type fake struct {
	t       *testing.T
	keyOf   func(args []string) string
	replies map[string]reply
	calls   []string
}

type reply struct {
	out string
	err error
}

func (f *fake) run(_ context.Context, args ...string) ([]byte, error) {
	k := f.keyOf(args)
	f.calls = append(f.calls, k)
	r, ok := f.replies[k]
	if !ok {
		f.t.Errorf("unexpected call %q", k)
		return nil, errors.New("unexpected call")
	}
	return []byte(r.out), r.err
}

// ghKey keys a gh call by the ref it asks about, after checking it is the GraphQL query.
func ghKey(t *testing.T) func([]string) string {
	return func(args []string) string {
		if len(args) < 2 || args[0] != "api" || args[1] != "graphql" {
			t.Errorf("gh %v: want gh api graphql", args)
			return ""
		}
		var owner, name, number string
		for i := 2; i+1 < len(args); i += 2 {
			k, v, _ := strings.Cut(args[i+1], "=")
			switch {
			case args[i] == "-f" && k == "query":
				if v != query {
					t.Errorf("gh query %q", v)
				}
			case args[i] == "-f" && k == "owner":
				owner = v
			case args[i] == "-f" && k == "name":
				name = v
			case args[i] == "-F" && k == "number":
				number = v
			default:
				t.Errorf("gh: unexpected flag %s %s", args[i], args[i+1])
			}
		}
		return owner + "/" + name + "#" + number
	}
}

func herdrKey(args []string) string { return strings.Join(args, " ") }

// issue and pr are gh's GraphQL replies.
func issue(state, reason, closer string) string {
	nodes := "[]"
	if closer != "" {
		nodes = `[{"closer":` + closer + `}]`
	}
	r := "null"
	if reason != "" {
		r = `"` + reason + `"`
	}
	return `{"data":{"repository":{"issueOrPullRequest":{"__typename":"Issue","state":"` + state +
		`","stateReason":` + r + `,"url":"https://github.com/o/r/issues/0","timelineItems":{"nodes":` + nodes + `}}}}}`
}

func pr(state, url string) string {
	return `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","state":"` + state +
		`","url":"` + url + `"}}}}`
}

const (
	byPR     = `{"__typename":"PullRequest","url":"https://github.com/o/r/pull/31"}`
	byCommit = `{"__typename":"Commit","url":"https://github.com/o/r/commit/abc1234"}`
)

// refreshNote has an item for each case refresh tells apart.
const refreshNote = "# Worklog — 2026-10-18\n" + `
## Next

1. [ ] Rotate the token · [[#^rotate|details]]

## 🔄 o/r

### 🔄 What remains

#### 🔄 Agent queue

- 🔄 [#1](https://github.com/o/r/issues/1): closed by a PR ^by-pr
- 🔍 [#2](https://github.com/o/r/issues/2): closed by a commit — PR up ^by-commit
- ⬜ [#3](https://github.com/o/r/issues/3): closed as not planned ^not-planned
- ⬜ [#4](https://github.com/o/r/issues/4): still open ^open
- 🔍 [#5](https://github.com/o/r/pull/5): a merged PR ^merged
- 🔍 [#6](https://github.com/o/r/pull/6): a PR closed unmerged ^unmerged
- 🔄 [#7](https://github.com/o/r/issues/7): its pane is gone <!-- qm pane=w1:p7 ws=/ws/7 --> ^gone
- 🔄 [#8](https://github.com/o/r/issues/8): its agent exited <!-- qm pane=w1:p8 ws=/ws/8 --> ^exited
- 🔄 [#9](https://github.com/o/r/issues/9): its agent works <!-- qm pane=w1:p9 ws=/ws/9 --> ^working
- ⏳ [#10](https://github.com/o/r/issues/10): waits, pane gone — on review <!-- qm pane=w1:p10 ws=/ws/10 --> ^waits
- ⏳ [#11](https://github.com/o/r/issues/11): waits, issue closed — on a person ^waits-closed
- ⬜ [#12](https://github.com/o/r/issues/12): closed with no reason ^no-reason
- ⬜ [#13](https://github.com/o/r/issues/13): gh fails ^gh-fails
- 🔄 [#14](https://github.com/o/r/issues/14): closed, its pane gone too <!-- qm pane=w1:p14 ws=/ws/14 --> ^closed-gone
- ✅ [#15](https://github.com/o/r/issues/15): done already <!-- qm pane=w1:p15 ws=/ws/15 --> ^done
- 🔄 no ref, nothing to check ^plain
- 🔄 mentions [#16](https://github.com/o/r/issues/16) after its text ^mention

#### ⬜ Your actions

- [x] Rotate the token ^rotate

#### ⏳ Sent to other repos

- ⏳ [infra#40](https://github.com/o/infra/issues/40): open the port → infra, **sent**, asked 10:00 ^port
- ⏳ [infra#41](https://github.com/o/infra/issues/41): a second ask → infra, **to send** ^unsent
- 🔄 [infra#42](https://github.com/o/infra/issues/42): a third ask → infra, **acked** ^third
- ⏳ [infra#43](https://github.com/o/infra/issues/43): a fourth ask → infra, **sent** ^fourth
`

func TestGatherAndApply(t *testing.T) {
	t.Parallel()
	gh := &fake{t: t, keyOf: ghKey(t), replies: map[string]reply{
		"o/r#1":      {out: issue("CLOSED", "COMPLETED", byPR)},
		"o/r#2":      {out: issue("CLOSED", "COMPLETED", byCommit)},
		"o/r#3":      {out: issue("CLOSED", "NOT_PLANNED", "")},
		"o/r#4":      {out: issue("OPEN", "", "")},
		"o/r#5":      {out: pr("MERGED", "https://github.com/o/r/pull/5")},
		"o/r#6":      {out: pr("CLOSED", "https://github.com/o/r/pull/6")},
		"o/r#7":      {out: issue("OPEN", "", "")},
		"o/r#8":      {out: issue("OPEN", "", "")},
		"o/r#9":      {out: issue("OPEN", "", "")},
		"o/r#10":     {out: issue("OPEN", "", "")},
		"o/r#11":     {out: issue("CLOSED", "COMPLETED", byPR)},
		"o/r#12":     {out: issue("CLOSED", "", "")},
		"o/r#13":     {err: errors.New("gh api graphql: HTTP 502")},
		"o/r#14":     {out: issue("CLOSED", "COMPLETED", "")},
		"o/infra#40": {out: issue("OPEN", "", "")},
		"o/infra#41": {out: issue("OPEN", "", "")},
		"o/infra#42": {out: issue("CLOSED", "COMPLETED", byPR)},
		"o/infra#43": {out: issue("CLOSED", "COMPLETED", byCommit)},
	}}
	herdr := &fake{t: t, keyOf: herdrKey, replies: map[string]reply{
		"pane get w1:p7": {err: &ExitError{Name: "herdr", Args: []string{"pane", "get", "w1:p7"},
			Stderr: []byte(`{"id":"1","error":{"code":"pane_not_found","message":"pane w1:p7 not found"}}`)}},
		"pane get w1:p8": {out: `{"id":"1","result":{"type":"pane_info","pane":{"pane_id":"w1:p8","agent":null,"agent_status":"unknown"}}}`},
		"pane get w1:p9": {out: `{"id":"1","result":{"type":"pane_info","pane":{"pane_id":"w1:p9","agent":"claude","agent_status":"working"}}}`},
		"workspace list": {out: `{"id":"1","result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"r"},{"workspace_id":"w5","label":"infra"}]}}`},
		"agent list": {out: `{"id":"1","result":{"type":"agent_list","agents":[` +
			`{"workspace_id":"w1","pane_id":"w1:p9","agent_status":"working"},` +
			`{"workspace_id":"w5","pane_id":"w5:p1","agent_status":"idle"}]}}`},
		"agent read w5:p1 --source recent-unwrapped --lines 400": {out: `{"id":"1","result":{"type":"pane_read","read":{"pane_id":"w5:p1","text":` +
			`"earlier work, https://github.com/o/infra/pull/1\n> open the port for https://github.com/o/infra/issues/40\n> When this is done, update the worklog with the worklog skill: work item ^port in /v/2026-10-18.md. Record the outcome and refresh Next.\nOpened the port in https://github.com/o/infra/pull/77 (merged), run: https://github.com/o/infra/actions/runs/9.\nSee also [the issue](https://github.com/o/infra/issues/40).\n"}}}`},
	}}
	src := []byte(refreshNote)
	n, err := note.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	plan := Reconciler{GH: gh.run, Herdr: herdr.run}.Gather(t.Context(), n)
	out, applied, skipped, err := plan.Apply(src)
	if err != nil {
		t.Fatal(err)
	}

	wantApplied := []Applied{
		{Item: "by-pr", Ref: "o/r#1", From: "IN_PROGRESS", To: "DONE", Summary: "closed by https://github.com/o/r/pull/31"},
		{Item: "by-commit", Ref: "o/r#2", From: "IN_REVIEW", To: "DONE", Summary: "closed by https://github.com/o/r/commit/abc1234"},
		{Item: "not-planned", Ref: "o/r#3", From: "TODO", To: "DROPPED", Summary: "closed as not planned: https://github.com/o/r/issues/0"},
		{Item: "merged", Ref: "o/r#5", From: "IN_REVIEW", To: "DONE", Summary: "merged https://github.com/o/r/pull/5"},
		{Item: "gone", Ref: "o/r#7", From: "IN_PROGRESS", To: "WAITING", Summary: "worker exited: pane w1:p7 is gone"},
		{Item: "exited", Ref: "o/r#8", From: "IN_PROGRESS", To: "WAITING", Summary: "worker exited: no agent in pane w1:p8"},
		{Item: "waits-closed", Ref: "o/r#11", From: "WAITING", To: "DONE", Summary: "closed by https://github.com/o/r/pull/31"},
		{Item: "closed-gone", Ref: "o/r#14", From: "IN_PROGRESS", To: "DONE", Summary: "closed as completed: https://github.com/o/r/issues/0"},
		{Item: "port", Ref: "o/infra#40", From: "WAITING", To: "WAITING", Status: "acked",
			Links: []string{"https://github.com/o/infra/pull/77", "https://github.com/o/infra/actions/runs/9"}},
		{Item: "third", Ref: "o/infra#42", From: "IN_PROGRESS", To: "DONE", Summary: "closed by https://github.com/o/r/pull/31", Status: "done"},
		{Item: "fourth", Ref: "o/infra#43", From: "WAITING", To: "DONE", Summary: "closed by https://github.com/o/r/commit/abc1234", Status: "done"},
	}
	if !slices.EqualFunc(applied, wantApplied, equalApplied) {
		t.Errorf("applied:\n%+v\nwant:\n%+v", applied, wantApplied)
	}
	wantSkipped := map[string]string{
		"open":      "o/r#4 is open",
		"unmerged":  "o/r#6 was closed without being merged",
		"working":   "o/r#9 is open; its worker claude in pane w1:p9 is working",
		"waits":     "o/r#10 is open",
		"no-reason": "o/r#12 is closed with no reason recorded",
		"gh-fails":  "gh: gh api graphql: HTTP 502",
		"unsent":    "o/infra#41 is open; not sent yet",
	}
	got := map[string]string{}
	for _, s := range skipped {
		got[s.Item] = s.Reason
	}
	for id, want := range wantSkipped {
		if got[id] != want {
			t.Errorf("^%s skipped %q, want %q", id, got[id], want)
		}
	}
	for id := range got {
		if _, ok := wantSkipped[id]; !ok {
			t.Errorf("^%s skipped unexpectedly: %q", id, got[id])
		}
	}
	// A terminal item, an item with no ref and one whose ref is not its leading link ask nothing.
	for _, c := range append(gh.calls, herdr.calls...) {
		if c == "o/r#15" || c == "o/r#16" || c == "pane get w1:p15" || c == "pane get w1:p10" || c == "pane get w1:p14" {
			t.Errorf("asked %q", c)
		}
	}

	want := refreshNote
	for _, r := range [][2]string{
		{"- 🔄 [#1](https://github.com/o/r/issues/1): closed by a PR ^by-pr",
			"- ✅ [#1](https://github.com/o/r/issues/1): closed by a PR — closed by https://github.com/o/r/pull/31 ^by-pr"},
		{"- 🔍 [#2](https://github.com/o/r/issues/2): closed by a commit — PR up ^by-commit",
			"- ✅ [#2](https://github.com/o/r/issues/2): closed by a commit — closed by https://github.com/o/r/commit/abc1234 ^by-commit"},
		{"- ⬜ [#3](https://github.com/o/r/issues/3): closed as not planned ^not-planned",
			"- ❌ [#3](https://github.com/o/r/issues/3): closed as not planned — closed as not planned: https://github.com/o/r/issues/0 ^not-planned"},
		{"- 🔍 [#5](https://github.com/o/r/pull/5): a merged PR ^merged",
			"- ✅ [#5](https://github.com/o/r/pull/5): a merged PR — merged https://github.com/o/r/pull/5 ^merged"},
		{"- 🔄 [#7](https://github.com/o/r/issues/7): its pane is gone <!-- qm pane=w1:p7 ws=/ws/7 --> ^gone",
			"- ⏳ [#7](https://github.com/o/r/issues/7): its pane is gone — worker exited: pane w1:p7 is gone <!-- qm pane=w1:p7 ws=/ws/7 --> ^gone"},
		{"- 🔄 [#8](https://github.com/o/r/issues/8): its agent exited <!-- qm pane=w1:p8 ws=/ws/8 --> ^exited",
			"- ⏳ [#8](https://github.com/o/r/issues/8): its agent exited — worker exited: no agent in pane w1:p8 <!-- qm pane=w1:p8 ws=/ws/8 --> ^exited"},
		{"- ⏳ [#11](https://github.com/o/r/issues/11): waits, issue closed — on a person ^waits-closed",
			"- ✅ [#11](https://github.com/o/r/issues/11): waits, issue closed — closed by https://github.com/o/r/pull/31 ^waits-closed"},
		{"- 🔄 [#14](https://github.com/o/r/issues/14): closed, its pane gone too <!-- qm pane=w1:p14 ws=/ws/14 --> ^closed-gone",
			"- ✅ [#14](https://github.com/o/r/issues/14): closed, its pane gone too — closed as completed: https://github.com/o/r/issues/0 <!-- qm pane=w1:p14 ws=/ws/14 --> ^closed-gone"},
		{"→ infra, **sent**, asked 10:00 ^port",
			"→ infra, **acked**, asked 10:00, https://github.com/o/infra/pull/77, https://github.com/o/infra/actions/runs/9 ^port"},
		{"- 🔄 [infra#42](https://github.com/o/infra/issues/42): a third ask → infra, **acked** ^third",
			"- ✅ [infra#42](https://github.com/o/infra/issues/42): a third ask → infra, **done** — closed by https://github.com/o/r/pull/31 ^third"},
		{"- ⏳ [infra#43](https://github.com/o/infra/issues/43): a fourth ask → infra, **sent** ^fourth",
			"- ✅ [infra#43](https://github.com/o/infra/issues/43): a fourth ask → infra, **done** — closed by https://github.com/o/r/commit/abc1234 ^fourth"},
	} {
		if strings.Count(want, r[0]) != 1 {
			t.Fatalf("no one %q in the note", r[0])
		}
		want = strings.Replace(want, r[0], r[1], 1)
	}
	if string(out) != want {
		t.Errorf("note:\n%s\nwant:\n%s", out, want)
	}
}

func equalApplied(a, b Applied) bool {
	return a.Item == b.Item && a.Ref == b.Ref && a.From == b.From && a.To == b.To && a.Summary == b.Summary &&
		a.Status == b.Status && slices.Equal(a.Links, b.Links)
}

// TestApplyChangedSinceRead skips an item whose state changed between the read the evidence
// was gathered on and the write.
func TestApplyChangedSinceRead(t *testing.T) {
	t.Parallel()
	const before = "# Worklog — 2026-10-18\n\n## 🔄 o/r\n\n### 🔄 What remains\n\n#### 🔄 Agent queue\n\n" +
		"- 🔄 [#1](https://github.com/o/r/issues/1): fix it ^fix\n"
	gh := &fake{t: t, keyOf: ghKey(t), replies: map[string]reply{"o/r#1": {out: issue("CLOSED", "COMPLETED", byPR)}}}
	n, err := note.Parse([]byte(before))
	if err != nil {
		t.Fatal(err)
	}
	plan := Reconciler{GH: gh.run, Herdr: (&fake{t: t, keyOf: herdrKey}).run}.Gather(t.Context(), n)
	now := strings.Replace(before, "🔄 [#1]", "⏳ [#1]", 1)
	out, applied, skipped, err := plan.Apply([]byte(now))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != now || len(applied) != 0 {
		t.Errorf("applied %+v:\n%s", applied, out)
	}
	if len(skipped) != 1 || skipped[0].Reason != "it moved from IN_PROGRESS to WAITING since refresh read it" {
		t.Errorf("skipped %+v", skipped)
	}
}

// TestApplyRefChanged skips an item whose leading ref was edited after the read.
func TestApplyRefChanged(t *testing.T) {
	t.Parallel()
	const before = "# Worklog — 2026-10-18\n\n## 🔄 o/r\n\n### 🔄 What remains\n\n#### 🔄 Agent queue\n\n" +
		"- 🔄 [#1](https://github.com/o/r/issues/1): fix it ^fix\n"
	gh := &fake{t: t, keyOf: ghKey(t), replies: map[string]reply{"o/r#1": {out: issue("CLOSED", "COMPLETED", byPR)}}}
	n, err := note.Parse([]byte(before))
	if err != nil {
		t.Fatal(err)
	}
	plan := Reconciler{GH: gh.run, Herdr: (&fake{t: t, keyOf: herdrKey}).run}.Gather(t.Context(), n)
	now := strings.Replace(before, "[#1](https://github.com/o/r/issues/1)", "[#2](https://github.com/o/r/issues/2)", 1)
	out, applied, skipped, err := plan.Apply([]byte(now))
	if err != nil || string(out) != now || len(applied) != 0 {
		t.Fatalf("applied %+v, err %v", applied, err)
	}
	if len(skipped) != 1 || skipped[0].Reason != "its ref changed from o/r#1 since refresh read it" {
		t.Errorf("skipped %+v", skipped)
	}
}

// TestNoRunners reports a missing gh or herdr per item, and changes nothing.
func TestNoRunners(t *testing.T) {
	t.Parallel()
	const src = "# Worklog — 2026-10-18\n\n## 🔄 o/r\n\n### 🔄 What remains\n\n#### 🔄 Agent queue\n\n" +
		"- 🔄 [#1](https://github.com/o/r/issues/1): fix it <!-- qm pane=w1:p1 ws=/ws --> ^fix\n"
	n, err := note.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out, applied, skipped, err := Reconciler{GH: Command("gh", "", nil), Herdr: Command("herdr", "", nil)}.
		Gather(t.Context(), n).Apply([]byte(src))
	if err != nil || string(out) != src || len(applied) != 0 {
		t.Fatalf("applied %+v, err %v", applied, err)
	}
	if want := "gh: gh not found on PATH; herdr: herdr not found on PATH"; len(skipped) != 1 || skipped[0].Reason != want {
		t.Errorf("skipped %+v, want %q", skipped, want)
	}
}

func TestReplyLinks(t *testing.T) {
	t.Parallel()
	text := "noise https://github.com/o/r/pull/1\n" +
		"> ... work item ^port in /v/n.md. Record it.\n" +
		"Done: [PR](https://github.com/o/infra/pull/7), https://github.com/o/infra/pull/7.\n" +
		"not GitHub: https://example.com/o/r/pull/8 and `https://github.com/o/infra/issues/9`\n"
	got, ok := replyLinks(text, "port")
	want := []string{"https://github.com/o/infra/pull/7", "https://github.com/o/infra/issues/9"}
	if !ok || !slices.Equal(got, want) {
		t.Errorf("links %v %v, want %v", got, ok, want)
	}
	if _, ok := replyLinks("no prompt here https://github.com/o/r/pull/1", "port"); ok {
		t.Error("a reply with no prompt for ^port")
	}
}

func TestCommand(t *testing.T) {
	t.Parallel()
	sh := "/bin/sh"
	out, err := Command("sh", sh, nil)(t.Context(), "-c", "echo out")
	if err != nil || string(out) != "out\n" {
		t.Fatalf("out %q, err %v", out, err)
	}
	const script = `echo '{"error":{"code":"pane_not_found"}}' >&2; exit 1`
	_, err = Command("sh", sh, nil)(t.Context(), "-c", script)
	var exit *ExitError
	if !errors.As(err, &exit) || string(exit.Stderr) != "{\"error\":{\"code\":\"pane_not_found\"}}\n" {
		t.Fatalf("err %v", err)
	}
	if err.Error() != "sh -c "+script+`: {"error":{"code":"pane_not_found"}}` || errorCode(err) != "pane_not_found" {
		t.Errorf("message %q", err)
	}
}

// TestReplyRoutes reads a reply from the workspace a ##### item's prompt names, over the label
// after its arrow, and from the pane a list item's dispatch marker names.
func TestReplyRoutes(t *testing.T) {
	t.Parallel()
	const src = "# Worklog — 2026-10-18\n\n## ⏳ o/r\n\n### ⏳ What remains\n\n#### ⏳ Sent to other repos\n\n" +
		"##### ⏳ open the port → other, **sent**\n\n" +
		"Body. [▶ send to infra](obsidian://shell-commands/?vault=w&execute=abc&_date=2026-10-18&_item=port) ^port\n\n" +
		"> [!prompt]- Prompt sent to infra\n> ```herdr-prompt id=port to=infra\n> open the port\n> ```\n\n" +
		"- ⏳ rotate the key → infra, **acked**, https://github.com/o/infra/pull/1 <!-- qm pane=w9:p2 ws=/ws --> ^key\n"
	read := func(id, links string) reply {
		return reply{out: `{"result":{"type":"pane_read","read":{"text":"> work item ^` + id + ` in /v.md.\n` + links + `\n"}}}`}
	}
	herdr := &fake{t: t, keyOf: herdrKey, replies: map[string]reply{
		"workspace list": {out: `{"result":{"workspaces":[{"workspace_id":"w5","label":"infra"},{"workspace_id":"w6","label":"other"}]}}`},
		"agent list": {out: `{"result":{"agents":[{"workspace_id":"w5","pane_id":"w5:p1","agent_status":"done"},` +
			`{"workspace_id":"w6","pane_id":"w6:p1","agent_status":"idle"},{"workspace_id":"w9","pane_id":"w9:p2","agent_status":"idle"}]}}`},
		"agent read w5:p1 --source recent-unwrapped --lines 400": read("port", "PR: https://github.com/o/infra/pull/5"),
		"agent read w9:p2 --source recent-unwrapped --lines 400": read("key", "https://github.com/o/infra/pull/1 and https://github.com/o/infra/pull/2"),
	}}
	n, err := note.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out, applied, skipped, err := Reconciler{GH: (&fake{t: t, keyOf: ghKey(t)}).run, Herdr: herdr.run}.Gather(t.Context(), n).Apply([]byte(src))
	if err != nil || len(skipped) != 0 || len(applied) != 2 {
		t.Fatalf("applied %+v skipped %+v err %v", applied, skipped, err)
	}
	want := strings.NewReplacer(
		"##### ⏳ open the port → other, **sent**\n", "##### ⏳ open the port → other, **acked**, https://github.com/o/infra/pull/5\n",
		"**acked**, https://github.com/o/infra/pull/1 <!-- qm pane=w9:p2 ws=/ws --> ^key",
		"**acked**, https://github.com/o/infra/pull/1, https://github.com/o/infra/pull/2 <!-- qm pane=w9:p2 ws=/ws --> ^key",
	).Replace(src)
	if string(out) != want {
		t.Errorf("note:\n%s\nwant:\n%s", out, want)
	}
}
