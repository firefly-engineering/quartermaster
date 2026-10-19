// Package refresh reconciles a day's note with GitHub and herdr (ADR 0003, spec §6 and §11):
// it moves a work item only where the evidence proves the move, and says why it left every
// other item it checked as it was.
//
// Refresh runs in two steps, so nothing outside the note runs under the note's lock:
// [Reconciler.Gather] reads a parsed note and asks gh and herdr about it, and [Plan.Apply],
// inside the engine's Mutate edit, applies what the evidence proved to the note as it then is,
// skipping an item that changed meanwhile. Ticked checkboxes need nothing here: every write
// reconciles them (spec §4.4), and so does refresh's, even when it moves nothing.
//
// The evidence, per work item that is not DONE or DROPPED:
//
//   - GitHub, for an item whose title leads with a link to an issue or PR: a merged PR is
//     DONE; an issue closed as completed is DONE, with the commit or PR that closed it; one
//     closed as not planned or as a duplicate is DROPPED. An open one, a PR closed unmerged or
//     an issue closed with no reason proves nothing. A WAITING item completes too, since what
//     it waited on is done: the merged PR, or what closed the issue, is its summary (§4.3).
//   - herdr, for an IN_PROGRESS item with a dispatch marker, which no transition has moved
//     since its dispatch: its pane gone, or no agent in it, is a worker that exited, and the
//     item goes to WAITING, never DONE.
//   - herdr, for an item in *Sent to other repos* marked **sent** or **acked**: the agent it
//     was sent to, once settled, has replied when its output after the item's prompt holds
//     GitHub links; the item is marked **acked** and the new links are added to its line. Its
//     state stays: only GitHub proves the work done, and when it does, the item's status
//     becomes **done** along with its state.
//
// One step writes outside the note, on a DONE or DROPPED item whose marker records a fresh
// dispatch (`pane=` and `ws=`): when its pane's agent is idle or done and still bears the name
// the dispatch gave it, or no agent runs in the pane, Gather closes the pane's herdr tab, and
// Apply drops `pane=` from the marker; `ws=` stays. A pane already gone just loses `pane=`. A
// running agent is left for the next refresh, and a renamed one keeps its tab: renaming it is
// how the developer keeps one. A session send's pane, with no `ws=`, is never closed.
//
// The next refresh, finding `ws=` alone, removes that checkout, and Apply drops `ws=`, the
// marker with it. A jj workspace is snapshotted, forgotten in its project and deleted; a git
// worktree is removed, never forced, so a dirty one stays. A checkout already gone is forgotten,
// or pruned. A removal that fails keeps `ws=` for the next refresh to retry. The checkout's
// issue-<N> branch is never deleted.
//
// Like every package but main, this one reads neither the environment nor the cwd: gh, jj and
// git come in as Runners, herdr as a herdr.Client, and the projects' checkouts from the
// configuration.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

// Reconciler gathers the evidence for a refresh.
type Reconciler struct {
	GH       Runner          // runs gh
	Herdr    herdr.Client    // calls herdr
	JJ, Git  Runner          // run jj and git, to remove a finished fresh checkout
	Projects config.Projects // where that checkout's project is
}

// Applied is one change refresh made to the note.
type Applied struct {
	Item    string   `json:"item" jsonschema:"the work item's id"`
	Ref     string   `json:"ref,omitempty" jsonschema:"its issue or PR, owner/repo#N"`
	From    string   `json:"from" jsonschema:"the state it was in"`
	To      string   `json:"to" jsonschema:"the state it is in; the same as from for a reply"`
	Summary string   `json:"summary,omitempty" jsonschema:"the evidence, written as its summary"`
	Status  string   `json:"status,omitempty" jsonschema:"an item sent to another repo: the status its reply set"`
	Links   []string `json:"links,omitempty" jsonschema:"an item sent to another repo: the links from its reply added to its line"`
	Pane    string   `json:"pane,omitempty" jsonschema:"a finished fresh dispatch: the pane dropped from its marker"`
	Tab     string   `json:"tab,omitempty" jsonschema:"a finished fresh dispatch: the herdr tab closed with it; absent when it was gone already"`
	// Workspace is set alone, on the refresh after the one that drops Pane.
	Workspace string `json:"ws,omitempty" jsonschema:"a finished fresh dispatch whose tab is closed: the checkout removed, or found gone, and dropped from its marker"`
}

// Skipped is a work item refresh checked and left as it was.
type Skipped struct {
	Item   string `json:"item" jsonschema:"the work item's id"`
	Ref    string `json:"ref,omitempty" jsonschema:"its issue or PR, owner/repo#N"`
	Reason string `json:"reason" jsonschema:"why it was left as it is: what each check found"`
}

// Plan is the evidence gathered from one read of a note: the changes it proves, and the
// items it proves nothing about.
type Plan struct {
	moves   []move
	skipped []Skipped
}

// move is one proven change: a transition, a sent status (Applied.Status), or both. A reply
// is a status with no transition: state is from. So is a closed dispatch tab (Applied.Pane).
type move struct {
	Applied
	from      note.State
	state     note.State // the state it moves to
	wasStatus string     // the sent status when read, which Status replaces
}

// gatherer is one Gather's state: what it asked once and reuses.
type gatherer struct {
	Reconciler
	refs       map[note.Ref]evidence
	agentList  []agent
	agentsErr  error
	agentsRead bool
}

// Gather asks GitHub and herdr about every work item of n that is not DONE or DROPPED, in note
// order, and returns what the answers prove. A DONE or DROPPED item's finished fresh dispatch
// has its tab closed here, and its pane dropped by Apply; once its pane is dropped, its
// checkout is removed here, and its ws dropped by Apply.
func (r Reconciler) Gather(ctx context.Context, n *note.Note) Plan {
	g := &gatherer{Reconciler: r, refs: map[note.Ref]evidence{}}
	var p Plan
	for _, pr := range n.Projects {
		for _, pt := range pr.Parts {
			for _, it := range pt.Items {
				g.item(ctx, &p, it, false)
			}
		}
		if pr.Remains == nil {
			continue
		}
		for _, b := range pr.Remains.Buckets {
			for _, it := range b.Items {
				g.item(ctx, &p, it, b.Name == note.SentBucket)
			}
		}
	}
	return p
}

// item checks one work item: GitHub first, as the strongest evidence, then its worker, then a
// reply. The first that proves a change wins.
func (g *gatherer) item(ctx context.Context, p *Plan, it *note.Item, sent bool) {
	from, _ := note.StateOf(it.Icon)
	if from == note.Done || from == note.Dropped {
		g.finished(ctx, p, it, from)
		return
	}
	head, _ := note.SplitSummary(it.Title)
	l := parseLine(it)
	m := move{Applied: Applied{Item: it.ID, From: from.String()}, from: from}
	if l.ref != (note.Ref{}) {
		m.Ref = l.ref.String()
	}
	var reasons []string
	checked := false
	if l.ref != (note.Ref{}) {
		checked = true
		e := g.github(ctx, l.ref)
		if e.to != 0 { // DONE or DROPPED, both legal from any open state with a summary
			m = m.transition(e.to, e.summary)
			if sent && e.to == note.Done && l.status != "" && l.status != statusDone {
				m.wasStatus, m.Status = l.status, statusDone // GitHub proves the ask done
			}
			p.moves = append(p.moves, m)
			return
		}
		reasons = append(reasons, e.reason)
	}
	// Only a fresh dispatch's worker (ws=) exiting moves its item: a session's pane, recorded by
	// a send, is the user's agent, and its leaving says nothing of the item.
	if it.Dispatch != nil && it.Dispatch.Pane != "" && it.Dispatch.Workspace != "" && from == note.InProgress {
		checked = true
		waiting, reason := g.worker(ctx, it.Dispatch.Pane)
		if waiting != "" {
			p.moves = append(p.moves, m.transition(note.Waiting, waiting))
			return
		}
		reasons = append(reasons, reason)
	}
	if sent {
		checked = true
		switch l.status {
		case statusSent, statusAcked:
			var pane string
			if it.Dispatch != nil {
				pane = it.Dispatch.Pane
			}
			links, reason := g.reply(ctx, it.ID, pane, head)
			if len(links) > 0 {
				m = m.transition(from, "")
				m.wasStatus, m.Status, m.Links = l.status, statusAcked, links
				p.moves = append(p.moves, m)
				return
			}
			reasons = append(reasons, reason)
		case statusToSend:
			reasons = append(reasons, "not sent yet")
		case statusDone:
			reasons = append(reasons, "its status is done")
		default:
			reasons = append(reasons, "no **sent** status in its line")
		}
	}
	if checked {
		p.skipped = append(p.skipped, Skipped{Item: it.ID, Ref: m.Ref, Reason: strings.Join(reasons, "; ")})
	}
}

// finished closes the herdr tab of a DONE or DROPPED item's fresh dispatch, when its worker is
// done with it, and plans dropping its pane from the item's marker. A marker with no ws is not a
// fresh dispatch's, so its pane is never closed. A marker with a ws alone, its tab closed, has
// its checkout removed, and plans dropping its ws.
func (g *gatherer) finished(ctx context.Context, p *Plan, it *note.Item, from note.State) {
	d := it.Dispatch
	if d == nil || d.Workspace == "" {
		return
	}
	m := move{Applied: Applied{Item: it.ID, From: from.String(), To: from.String()}, from: from, state: from}
	if l := parseLine(it); l.ref != (note.Ref{}) {
		m.Ref = l.ref.String()
	}
	var reason string
	if d.Pane != "" {
		m.Pane = d.Pane
		m.Tab, reason = g.closeTab(ctx, d.Pane, d.Workspace)
	} else {
		m.Workspace = d.Workspace
		reason = g.removeCheckout(ctx, d.Workspace)
	}
	if reason != "" {
		p.skipped = append(p.skipped, Skipped{Item: it.ID, Ref: m.Ref, Reason: reason})
		return
	}
	p.moves = append(p.moves, m)
}

// transition is m as a transition to state s with summary.
func (m move) transition(s note.State, summary string) move {
	m.To, m.state, m.Summary = s.String(), s, summary
	return m
}

// Apply applies the plan to src, the note as it is now, one change at a time and in note
// order. A change to an item that is gone, or whose state, ref or sent status changed since the
// plan's read, is skipped. It returns the new source, what it applied, and every item it
// skipped, the plan's included.
func (p Plan) Apply(src []byte) (out []byte, applied []Applied, skipped []Skipped, err error) {
	out = src
	skipped = append(skipped, p.skipped...)
	for _, m := range p.moves {
		next, err := m.apply(out)
		if err != nil {
			skipped = append(skipped, Skipped{Item: m.Item, Ref: m.Ref, Reason: err.Error()})
			continue
		}
		out = next
		applied = append(applied, m.Applied)
	}
	return out, applied, skipped, nil
}

// apply makes m's change to src, after checking the item still reads as it did.
func (m move) apply(src []byte) ([]byte, error) {
	n, err := note.Parse(src)
	if err != nil {
		return nil, err
	}
	it := n.Item(m.Item)
	if it == nil {
		return nil, errors.New("it is gone since refresh read it")
	}
	if now, _ := note.StateOf(it.Icon); now != m.from {
		return nil, fmt.Errorf("it moved from %s to %s since refresh read it", m.from, now)
	}
	l := parseLine(it)
	if m.Ref != "" && l.ref.String() != m.Ref {
		return nil, fmt.Errorf("its ref changed from %s since refresh read it", m.Ref)
	}
	if m.Status != "" && l.status != m.wasStatus {
		return nil, fmt.Errorf("its status changed from %s to %s since refresh read it", m.wasStatus, l.status)
	}
	if m.Pane != "" {
		if it.Dispatch == nil || it.Dispatch.Pane != m.Pane {
			return nil, fmt.Errorf("its marker no longer records pane %s", m.Pane)
		}
		d := *it.Dispatch
		d.Pane = ""
		return note.SetDispatch(n, src, m.Item, d)
	}
	if m.Workspace != "" {
		if it.Dispatch == nil || it.Dispatch.Workspace != m.Workspace {
			return nil, fmt.Errorf("its marker no longer records ws %s", m.Workspace)
		}
		d := *it.Dispatch
		d.Workspace = ""
		return note.SetDispatch(n, src, m.Item, d)
	}
	if m.state != m.from {
		if src, err = note.TransitionItem(n, src, m.Item, m.state, m.Summary); err != nil {
			return nil, err
		}
		if m.Status == "" {
			return src, nil
		}
		if n, err = note.Parse(src); err != nil {
			return nil, err
		}
		it = n.Item(m.Item)
	}
	return setStatus(src, it, m.Status, m.Links)
}

// setStatus writes status as item it's sent status, and adds links at the end of its note,
// before its summary. The title line is rendered as a transition renders it, so its marker,
// indent and nested lines stay.
func setStatus(src []byte, it *note.Item, status string, links []string) ([]byte, error) {
	if it.Sent == nil {
		return nil, errors.New("it has no sent status to set")
	}
	u := it.TitleUnit()
	u.Sent.Status = status
	if len(links) > 0 {
		u.Sent.Note = strings.TrimPrefix(u.Sent.Note+", "+strings.Join(links, ", "), ", ")
	}
	line, err := note.Renderer{}.ItemTitle(u)
	if err != nil {
		return nil, err
	}
	return note.Replace(src, note.Span{Start: it.IconSpan.Start, End: it.Head.End}, line), nil
}
