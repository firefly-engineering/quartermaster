package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/refresh"
)

// ghFake answers gh's question about each ref, keyed `owner/repo#N`, with its JSON. A question
// it has no answer for fails the test.
func ghFake(t *testing.T, answers map[string]string) refresh.Runner {
	return func(_ context.Context, args ...string) ([]byte, error) {
		var owner, name, number string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "owner="); ok {
				owner = v
			} else if v, ok := strings.CutPrefix(a, "name="); ok {
				name = v
			} else if v, ok := strings.CutPrefix(a, "number="); ok {
				number = v
			}
		}
		ref := owner + "/" + name + "#" + number
		out, ok := answers[ref]
		if !ok {
			t.Errorf("gh asked about %s", ref)
			return nil, errors.New("unexpected")
		}
		return []byte(out), nil
	}
}

// noHerdr fails the test if herdr is run.
func noHerdr(t *testing.T) refresh.Runner {
	return func(_ context.Context, args ...string) ([]byte, error) {
		t.Errorf("herdr %v", args)
		return nil, errors.New("unexpected")
	}
}

const (
	openIssue = `{"data":{"repository":{"issueOrPullRequest":{"__typename":"Issue","state":"OPEN","url":"https://github.com/acme/web/issues/14"}}}}`
	mergedPR  = `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","state":"MERGED","url":"https://github.com/acme/web/pull/12"}}}}`
	openPR    = `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","state":"OPEN","url":"https://github.com/acme/web/pull/12"}}}}`
)

// TestRefreshSettles refreshes a note that GitHub proves nothing about: the write still ticks
// the action paired with a ticked Next line and derives the icons, and commits that.
func TestRefreshSettles(t *testing.T) {
	t.Parallel()
	const nextLine = "1. [ ] Rotate the staging token · 5 min · [[#^rotate-token|details]]"
	d := fixtureDeps(t, strings.Replace(string(readFixture(t)), nextLine, strings.Replace(nextLine, "[ ]", "[x]", 1), 1), true)
	rec := refresh.Reconciler{GH: ghFake(t, map[string]string{"acme/web#14": openIssue, "acme/web#12": openPR}), Herdr: noHerdr(t)}
	out, err := d.refreshWith(t.Context(), RefreshInput{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || !out.Committed || len(out.Applied) != 0 || len(out.SkippedItems) != 2 ||
		out.SkippedItems[0] != (refresh.Skipped{Item: "session-store", Ref: "acme/web#14", Reason: "acme/web#14 is open"}) ||
		out.SkippedItems[1] != (refresh.Skipped{Item: "login-stack", Ref: "acme/web#12", Reason: "acme/web#12 is open"}) {
		t.Errorf("out %+v", out)
	}
	d.checkNote(t, edited(t, nextLine, strings.Replace(nextLine, "[ ]", "[x]", 1),
		"- [ ] Rotate the staging token ^rotate-token", "- [x] Rotate the staging token ^rotate-token",
		"#### 🔄 Your actions", "#### ✅ Your actions"))
	if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
		"worklog(2026-10-16): refresh: no evidence of a change\n") {
		t.Errorf("commit %q", log)
	}
}

// TestRefreshMoves applies what GitHub proves, as one write.
func TestRefreshMoves(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), true)
	rec := refresh.Reconciler{GH: ghFake(t, map[string]string{"acme/web#14": openIssue, "acme/web#12": mergedPR}), Herdr: noHerdr(t)}
	out, err := d.refreshWith(t.Context(), RefreshInput{Date: fixtureDate}, rec)
	if err != nil {
		t.Fatal(err)
	}
	want := refresh.Applied{Item: "login-stack", Ref: "acme/web#12", From: "IN_REVIEW", To: "DONE",
		Summary: "merged https://github.com/acme/web/pull/12"}
	if !out.Changed || !out.Committed || len(out.Applied) != 1 || out.Applied[0].Item != want.Item ||
		out.Applied[0].Summary != want.Summary || out.Applied[0].To != want.To || len(out.SkippedItems) != 1 {
		t.Errorf("out %+v", out)
	}
	d.checkNote(t, edited(t,
		"- 🔍 [#12](https://github.com/acme/web/pull/12) login stack, in review ^login-stack",
		"- ✅ [#12](https://github.com/acme/web/pull/12) login stack, in review — merged https://github.com/acme/web/pull/12 ^login-stack",
		"#### 🔄 Agent queue", "#### ✅ Agent queue"))
	if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
		"worklog(2026-10-16): refresh: ^login-stack IN_REVIEW to DONE\n") {
		t.Errorf("commit %q", log)
	}
}

// TestRefreshRefusesUnparseable never writes a note that does not parse.
func TestRefreshRefusesUnparseable(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t)+"\n##### ⬜ stray\n", false)
	if _, err := d.refreshWith(t.Context(), RefreshInput{}, refresh.Reconciler{GH: noHerdr(t), Herdr: noHerdr(t)}); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("err %v", err)
	}
}
