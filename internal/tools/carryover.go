package tools

import (
	"cmp"
	"slices"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// carryOver collects every open item of the previous note, prev of date, as a carry-over row
// with no verdict, in note order (spec §6, the skill's Open phase):
//
//   - each objective not DONE or DROPPED: its cells as written;
//   - each What remains work item, or entry led by a state icon, not DONE or DROPPED;
//   - each unticked manual action, except a Next checkbox whose details link names a manual
//     action or work item, since that one is collected in its own right;
//   - each carry-over row still open (CarryOverRow.Open), as a deferred one or one never
//     given a verdict;
//   - each Process entry: the parser leaves out its checkboxes (manual actions) and `—`.
//
// A Part holds what was done, so its items are not collected; its manual actions are. The
// renderer formats each row.
func carryOver(prev *note.Note, date string, r note.Renderer) ([]note.CarryOverUnit, error) {
	type carried struct {
		line int
		c    note.Carried
	}
	var all []carried
	add := func(line int, c note.Carried) { all = append(all, carried{line, c}) }

	if prev.Objectives != nil {
		for _, o := range prev.Objectives.Rows {
			if open(o.Icon) {
				add(o.Line, note.Carried{Text: o.Objective, StateText: o.Progress})
			}
		}
	}

	for _, p := range prev.Projects {
		if p.Remains == nil {
			continue
		}
		for _, b := range p.Remains.Buckets {
			for _, it := range b.Items {
				if s, _ := note.StateOf(it.Icon); open(it.Icon) {
					add(it.Line, note.Carried{Text: it.Title, Repo: p.Repo, ID: it.ID, Where: b.Name, State: s})
				}
			}
			for _, e := range b.Entries {
				if s, _ := note.StateOf(e.Icon); open(e.Icon) {
					add(e.Line, note.Carried{Text: e.Text, Repo: p.Repo, Where: b.Name, State: s})
				}
			}
		}
	}

	ids := map[string]bool{}
	for _, a := range prev.Actions {
		if a.ID != "" {
			ids[a.ID] = true
		}
	}
	paired := map[int]bool{} // Next lines whose checkbox is collected through what they link
	if prev.Next != nil {
		for _, it := range prev.Next.Items {
			if it.Ref != "" && (ids[it.Ref] || prev.Item(it.Ref) != nil) {
				paired[it.Line] = true
			}
		}
	}
	for _, a := range prev.Actions {
		if a.Checked || paired[a.Line] {
			continue
		}
		c := note.Carried{Text: a.Text, ID: a.ID, State: note.Todo, Manual: true}
		if p := projectAt(prev, a.Start); p != nil {
			c.Repo = p.Repo
			if b := bucketAt(p, a.Start); b != nil {
				c.Where = b.Name
			}
		} else if prev.Process != nil && within(a.Span, prev.Process.Span) {
			c.Where = note.ProcessSection
		}
		add(a.Line, c)
	}

	if prev.CarryOver != nil {
		for _, row := range prev.CarryOver.Rows {
			if row.Open() {
				add(row.Line, note.Carried{Text: row.Item, StateText: row.State})
			}
		}
	}

	if prev.Process != nil {
		for _, e := range prev.Process.Entries {
			add(e.Line, note.Carried{Text: e.Text, Where: note.ProcessSection, State: note.Todo})
		}
	}

	slices.SortStableFunc(all, func(a, b carried) int { return cmp.Compare(a.line, b.line) })
	out := make([]note.CarryOverUnit, len(all))
	for i, x := range all {
		u, err := r.CarriedRow(date, x.c)
		if err != nil {
			return nil, err
		}
		out[i] = u
	}
	return out, nil
}

// open reports whether an icon is a state other than DONE or DROPPED.
func open(i note.Icon) bool {
	s, ok := note.StateOf(i)
	return ok && s != note.Done && s != note.Dropped
}

// projectAt is the project whose section holds offset off, or nil.
func projectAt(n *note.Note, off int) *note.Project {
	for _, p := range n.Projects {
		if off >= p.Start && off < p.End {
			return p
		}
	}
	return nil
}

// bucketAt is the bucket of project p whose section holds offset off, or nil.
func bucketAt(p *note.Project, off int) *note.Bucket {
	if p.Remains == nil {
		return nil
	}
	for _, b := range p.Remains.Buckets {
		if off >= b.Start && off < b.End {
			return b
		}
	}
	return nil
}

func within(s, section note.Span) bool { return s.Start >= section.Start && s.End <= section.End }
