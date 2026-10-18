package tools

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// ItemFields are a work item as item_upsert writes it, and as carryover_verdict's keep does.
type ItemFields struct {
	ID     string       `json:"id" jsonschema:"the item id: lowercase letters, digits and -, unique in the note"`
	Repo   string       `json:"repo,omitempty" jsonschema:"its project, owner/repo; needed to create, unchanged when absent"`
	Bucket string       `json:"bucket,omitempty" jsonschema:"its bucket: Decisions for you, Your actions, Agent queue, Sent to other repos, Outside input: chase or Waiting; needed to create, unchanged when absent"`
	Title  string       `json:"title,omitempty" jsonschema:"one line, after its icon and ref link, without the separator ' — '; needed to create"`
	Ref    *string      `json:"ref,omitempty" jsonschema:"the issue or PR it is about, owner/repo#N, or #N in its project; rendered as a full link. On update unchanged when absent; null clears it"`
	Cost   *string      `json:"cost,omitempty" jsonschema:"what it costs the user, as: 20 min. On update unchanged when absent; null clears it"`
	Route  *string      `json:"route,omitempty" jsonschema:"the next skill that moves it, as: /implement. On update unchanged when absent; null clears it"`
	Body   string       `json:"body,omitempty" jsonschema:"prose under its heading, with no blank line; makes it a ##### item"`
	Prompt *PromptInput `json:"prompt,omitempty" jsonschema:"the prompt its ▶ button sends; makes it a ##### item. An item_upsert update of an item with a prompt refuses an absent one; null drops its prompt and its ▶ buttons, in its bucket and on Next, and it stays a ##### item only with a body"`
	Button string       `json:"button,omitempty" jsonschema:"the ▶ label, as: grill in trunk; with a prompt, send to <to> when absent"`
	Sent   string       `json:"sent,omitempty" jsonschema:"in Sent to other repos, how far its ask has gone: to send, sent, acked or done; written after its title as → <sent_to>, **<sent>**; unchanged when absent"`
	SentTo string       `json:"sent_to,omitempty" jsonschema:"the herdr workspace label its ask goes to, written after the →; unchanged when absent, else the prompt's to"`

	// null holds the fields given as an explicit JSON null, which decodes as an absent one does.
	// An update drops a null prompt and its buttons, and clears a null ref, cost or route.
	null struct{ prompt, ref, cost, route bool }
}

// PromptInput is a work item's herdr-prompt block.
type PromptInput struct {
	To   string `json:"to" jsonschema:"the herdr workspace label of the session it goes to"`
	Text string `json:"text" jsonschema:"the self-contained prompt"`
}

// ItemUpsertInput is item_upsert's input. An update rewrites the item from these fields; its
// state, summary and place, unless repo or bucket move it, stay as they are, and so do its ref,
// cost and route when absent or "". It refuses to drop a ##### item's body and prompt by leaving
// them out; an explicit null prompt drops the prompt, and a null ref, cost or route clears it.
type ItemUpsertInput struct {
	Date string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	ItemFields
	State string `json:"state,omitempty" jsonschema:"a new item's state, TODO when absent; item_transition moves an existing one"`
}

// UnmarshalJSON decodes in, refusing unknown fields, and tells an explicit null prompt, ref,
// cost or route from an absent one, which decode alike into a nil pointer.
func (in *ItemUpsertInput) UnmarshalJSON(b []byte) error {
	type plain ItemUpsertInput // in's fields, without this method
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode((*plain)(in)); err != nil {
		return err
	}
	var raw struct {
		Prompt, Ref, Cost, Route json.RawMessage
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	isNull := func(m json.RawMessage) bool { return string(m) == "null" }
	in.null.prompt, in.null.ref = isNull(raw.Prompt), isNull(raw.Ref)
	in.null.cost, in.null.route = isNull(raw.Cost), isNull(raw.Route)
	return nil
}

// ItemOutput is a work item where it now is.
type ItemOutput struct {
	Repo   string   `json:"repo" jsonschema:"its project"`
	Bucket string   `json:"bucket" jsonschema:"its bucket, absent in a Part"`
	Item   ItemView `json:"item"`
}

// ItemUpsertOutput is item_upsert's result.
type ItemUpsertOutput struct {
	Written
	Created bool `json:"created" jsonschema:"the item is new"`
	ItemOutput
	Warnings []string `json:"warnings,omitempty" jsonschema:"one line each: what the item as written lacks, as a TODO item with a /… route and no prompt, which has no ▶ button; the write went through"`
}

// itemUpsert creates a work item in a bucket, or rewrites one in place or into another bucket.
func (d Deps) itemUpsert(ctx context.Context, in ItemUpsertInput) (ItemUpsertOutput, error) {
	created := false
	res, w, err := d.write(ctx, in.Date, "item_upsert", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		it := n.Item(in.ID)
		created = it == nil
		if created {
			s := note.Todo
			if in.State != "" {
				var err error
				if s, err = note.ParseState(in.State); err != nil {
					return nil, "", err
				}
			}
			out, err := createItem(n, src, r, in.ItemFields, s.Icon(), "")
			return out, "add ^" + in.ID + " to " + in.Repo + ", " + in.Bucket, err
		}
		if in.State != "" {
			return nil, "", fmt.Errorf("^%s exists: item_transition moves it, item_upsert keeps its state", in.ID)
		}
		out, headline, err := updateItem(n, src, r, it, in.ItemFields)
		if err != nil || !in.null.prompt {
			return out, headline, err
		}
		out, err = dropNextButtons(out, in.ID)
		return out, headline, err
	})
	if err != nil {
		return ItemUpsertOutput{}, err
	}
	o := ItemUpsertOutput{Written: w, Created: created, ItemOutput: itemOutput(res.Note, in.ID)}
	if it := res.Note.Item(in.ID); noButton(it) {
		o.Warnings = []string{noButtonWarning(it)}
	}
	return o, nil
}

// noButton reports whether work item it is TODO and routed to a slash command, but has no
// prompt: its ▶ button would send that command, and with no prompt it has none.
func noButton(it *note.Item) bool {
	s, _ := note.StateOf(it.Icon)
	return s == note.Todo && strings.HasPrefix(it.Route, "/") && it.Prompt == nil
}

// noButtonWarning is the warning for work item it, which noButton reports.
func noButtonWarning(it *note.Item) string {
	return fmt.Sprintf("^%s is routed to %s but has no ▶ button: give it a prompt", it.ID, it.Route)
}

// dropNextButtons removes the ▶ button from each *Next* line linking work item id, whose prompt
// is gone: a button sends the item's prompt, so it would send nothing.
func dropNextButtons(src []byte, id string) ([]byte, error) {
	n, err := reparse(src)
	if err != nil || n.Next == nil {
		return src, err
	}
	for _, x := range slices.Backward(n.Next.Items) {
		if x.Ref == id && x.Button.End > x.Button.Start {
			src = note.Replace(src, x.Button, nil)
		}
	}
	return src, nil
}

// itemOutput is work item id of n, where it is.
func itemOutput(n *note.Note, id string) ItemOutput {
	it := n.Item(id)
	o := ItemOutput{Item: itemView(it)}
	if p := projectAt(n, it.Start); p != nil {
		o.Repo = p.Repo
		if b := bucketAt(p, it.Start); b != nil {
			o.Bucket = b.Name
		}
	}
	return o
}

// unit builds the renderer's unit for f in project repo, with its icon and summary.
func unit(f ItemFields, repo string, icon note.Icon, summary string) (note.ItemUnit, error) {
	u := note.ItemUnit{
		ID: f.ID, Icon: icon, Title: strings.TrimSpace(f.Title), Repo: repo, Cost: value(f.Cost), Route: value(f.Route),
		Summary: summary, Body: f.Body, Heading: f.Body != "" || f.Prompt != nil,
	}
	if s := value(f.Ref); s != "" {
		ref, err := note.ParseRef(s, repo)
		if err != nil {
			return note.ItemUnit{}, err
		}
		u.Ref = ref
	}
	if f.Prompt != nil {
		u.Prompt = &note.PromptUnit{To: f.Prompt.To, Body: f.Prompt.Text}
		u.Button = f.Button
		if u.Button == "" {
			u.Button = note.DefaultButton(f.Prompt.To)
		}
	} else if f.Button != "" {
		return note.ItemUnit{}, fmt.Errorf("^%s: a button needs a prompt to send", f.ID)
	}
	switch {
	case f.Sent == "" && f.SentTo != "":
		return note.ItemUnit{}, fmt.Errorf("^%s: sent_to goes with a sent status", f.ID)
	case f.Sent == "":
	case f.Bucket != note.SentBucket:
		return note.ItemUnit{}, fmt.Errorf("^%s: a sent status is for an item in %s, not %s", f.ID, note.SentBucket, f.Bucket)
	default:
		u.Sent = &note.SentUnit{To: f.SentTo, Status: f.Sent}
		if u.Sent.To == "" && f.Prompt != nil {
			u.Sent.To = f.Prompt.To
		}
		if u.Sent.To == "" {
			return note.ItemUnit{}, fmt.Errorf("^%s: a sent status needs the workspace label it goes to: give sent_to or a prompt", f.ID)
		}
	}
	return u, nil
}

// value is *p, "" when p is nil.
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// kept is the value of a field an update gives as v: v itself, else, when v is absent or "",
// the item's current one, was, unless v is an explicit null, which clears it.
func kept(v *string, was string, null bool) *string {
	if null || value(v) != "" {
		return v
	}
	return &was
}

// createItem writes f as a new work item with icon and summary at the end of its bucket,
// creating the project section, its What remains or the bucket when the note lacks them.
func createItem(n *note.Note, src []byte, r note.Renderer, f ItemFields, icon note.Icon, summary string) ([]byte, error) {
	if !note.ValidID(f.ID) {
		return nil, fmt.Errorf("invalid item id ^%s: lowercase letters, digits and - only", f.ID)
	}
	if f.Repo == "" || f.Bucket == "" || strings.TrimSpace(f.Title) == "" {
		return nil, fmt.Errorf("a new work item ^%s needs its repo, bucket and title", f.ID)
	}
	if usedID(n, f.ID) {
		return nil, fmt.Errorf("^%s already names a manual action or anchor in the note", f.ID)
	}
	if !slices.Contains(note.BucketNames, f.Bucket) {
		return nil, fmt.Errorf("unknown bucket %q: want one of %s", f.Bucket, strings.Join(note.BucketNames, ", "))
	}
	u, err := unit(f, f.Repo, icon, summary)
	if err != nil {
		return nil, err
	}
	return insertItem(n, src, r, f.Repo, f.Bucket, u)
}

// usedID reports whether id names a manual action or an anchor.
func usedID(n *note.Note, id string) bool {
	return slices.ContainsFunc(n.Actions, func(a note.ManualAction) bool { return a.ID == id }) ||
		slices.ContainsFunc(n.Anchors, func(a note.Anchor) bool { return a.ID == id })
}

// updateItem rewrites work item it from f, keeping its icon and summary. In its bucket it is
// rewritten in place; a list item keeps its marker, indent and nested lines. Given another
// repo or bucket, or a change of form, it moves to the end of its bucket.
func updateItem(n *note.Note, src []byte, r note.Renderer, it *note.Item, f ItemFields) ([]byte, string, error) {
	p := projectAt(n, it.Start)
	b := bucketAt(p, it.Start)
	if b == nil {
		return nil, "", fmt.Errorf("^%s is in a Part, which records what was done: item_upsert writes items in buckets", f.ID)
	}
	if f.Repo == "" {
		f.Repo = p.Repo
	}
	if f.Bucket == "" {
		f.Bucket = b.Name
	}
	// Its ref, cost and route stay when absent, as its repo and bucket do; a null clears one.
	f.Ref, f.Cost, f.Route = kept(f.Ref, it.Ref, f.null.ref), kept(f.Cost, it.Cost, f.null.cost),
		kept(f.Route, it.Route, f.null.route)
	if strings.TrimSpace(f.Title) == "" {
		return nil, "", fmt.Errorf("^%s: an update rewrites the item, so it needs its title", f.ID)
	}
	// Its sent status stays while it stays in Sent to other repos, as its state does.
	if s := it.Sent; s != nil && f.Bucket == note.SentBucket {
		f.Sent = cmp.Or(f.Sent, s.Status)
		f.SentTo = cmp.Or(f.SentTo, s.To)
	}
	_, summary := note.SplitSummary(it.Title)
	u, err := unit(f, f.Repo, it.Icon, summary)
	if err != nil {
		return nil, "", err
	}
	if u.Sent != nil && it.Sent != nil {
		u.Sent.Note = it.Sent.Note // refresh's record of the reply, which item_upsert does not take
	}
	u.Dispatch = it.Dispatch // task_dispatch's record, which item_upsert does not take
	// An update rewrites the item whole, and the note is all there is of its prose and prompt.
	// A null prompt drops its prompt on purpose, and with no body it becomes a list item.
	dropsItsPrompt := f.null.prompt && it.Prompt != nil
	if it.Heading && !u.Heading && !dropsItsPrompt {
		return nil, "", fmt.Errorf("^%s is a ##### item, which an update rewrites: give its body or prompt, or it would lose them", f.ID)
	}
	if it.Prompt != nil && u.Prompt == nil && !dropsItsPrompt {
		return nil, "", fmt.Errorf("^%s has a prompt, which an update rewrites: give it again, or it would be lost", f.ID)
	}
	if f.Repo == p.Repo && f.Bucket == b.Name && u.Heading == it.Heading {
		if it.Heading {
			out, err := r.Item(u)
			return note.Replace(src, it.Span, out), "update ^" + f.ID, err
		}
		out, err := r.ItemTitle(u)
		return note.Replace(src, note.Span{Start: it.IconSpan.Start, End: it.Head.End}, out), "update ^" + f.ID, err
	}
	if !it.Heading && it.Head.End != it.End {
		return nil, "", fmt.Errorf("^%s has nested lines, which a move would lose: move it by hand", f.ID)
	}
	if !slices.Contains(note.BucketNames, f.Bucket) {
		return nil, "", fmt.Errorf("unknown bucket %q: want one of %s", f.Bucket, strings.Join(note.BucketNames, ", "))
	}
	// Its *Next* lines stay: halfway they link an id the note lacks, and once it is back they
	// link it again.
	out, err := removeItem(src, it, b)
	if err != nil {
		return nil, "", err
	}
	if n, err = reparseIntermediate(out); err != nil {
		return nil, "", err
	}
	out, err = insertItem(n, out, r, f.Repo, f.Bucket, u)
	return out, "move ^" + f.ID + " to " + f.Repo + ", " + f.Bucket, err
}

// removeItem removes work item it from bucket b, putting back `- —` when it leaves b with nothing.
// *Next* may still link it: the caller puts it back or drops those lines.
func removeItem(src []byte, it *note.Item, b *note.Bucket) ([]byte, error) {
	out := note.Remove(src, it.Span)
	n, err := reparseIntermediate(out)
	if err != nil {
		return nil, err
	}
	for _, p := range n.Projects {
		if p.Remains == nil {
			continue
		}
		for _, nb := range p.Remains.Buckets {
			if nb.Start == b.Start && nb.End == nb.Heading.End {
				return note.InsertBlock(out, nb.End, []byte(note.NothingLine)), nil
			}
		}
	}
	return out, nil
}

// insertItem writes u at the end of bucket of project repo, in place of its `- —` when it has
// nothing else. A note without the project gets its section, after the last project, and lists
// it in its front matter; a project
// without What remains or the bucket gets it, the buckets in template order.
func insertItem(n *note.Note, src []byte, r note.Renderer, repo, bucket string, u note.ItemUnit) ([]byte, error) {
	var buckets []note.BucketUnit // every bucket, u in its own, for a new section
	for _, name := range note.BucketNames {
		b := note.BucketUnit{Icon: note.IconNone, Name: name}
		if name == bucket {
			b.Icon, b.Items = u.Icon, []note.ItemUnit{u}
		}
		buckets = append(buckets, b)
	}
	i := slices.IndexFunc(n.Projects, func(p *note.Project) bool { return p.Repo == repo })
	if i < 0 {
		section, err := r.Project(note.ProjectUnit{Icon: note.IconTodo, Repo: repo, Remains: note.IconTodo, Buckets: buckets})
		if err != nil {
			return nil, err
		}
		// The section goes after the front matter, so n's span of it still holds.
		return note.ListProject(note.InsertBlock(src, newProjectAt(n, src), section), n.FrontMatter, repo)
	}
	p := n.Projects[i]
	if p.Remains == nil {
		remains, err := r.Remains(note.IconTodo, buckets)
		if err != nil {
			return nil, err
		}
		return note.InsertBlock(src, p.End, remains), nil
	}
	k := slices.IndexFunc(p.Remains.Buckets, func(b *note.Bucket) bool { return b.Name == bucket })
	if k < 0 {
		b, err := r.Bucket(note.BucketUnit{Icon: u.Icon, Name: bucket, Items: []note.ItemUnit{u}})
		if err != nil {
			return nil, err
		}
		return note.InsertBlock(src, newBucketAt(p.Remains, bucket), b), nil
	}
	line, err := r.Item(u)
	if err != nil {
		return nil, err
	}
	return appendToBucket(src, p.Remains.Buckets[k], line, u.Heading), nil
}

// newProjectAt is where a new project section goes: after the last one, else before
// Carry-over, else before Process, else at the end.
func newProjectAt(n *note.Note, src []byte) int {
	switch {
	case len(n.Projects) > 0:
		return n.Projects[len(n.Projects)-1].End
	case n.CarryOver != nil:
		return n.CarryOver.Start
	case n.Process != nil:
		return n.Process.Start
	}
	return len(src)
}

// ItemTransitionInput is item_transition's input.
type ItemTransitionInput struct {
	Date    string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Item    string `json:"item" jsonschema:"the work item's id"`
	To      string `json:"to" jsonschema:"the state to move it to: TODO, IN_PROGRESS, IN_REVIEW, WAITING, DONE or DROPPED; its own state, in any state, rewrites only its summary"`
	Summary string `json:"summary,omitempty" jsonschema:"one line: the PR URL, commit, evidence or reason; needed to wait, to drop, to complete from IN_PROGRESS or WAITING, and to move to its own state; the item's last one stays when absent"`
	Part    int    `json:"part,omitempty" jsonschema:"with DONE or DROPPED, the Part N of its project it is filed into: it leaves its bucket and Next, and becomes a plain line there with its summary, without its id, button or prompt; it stays in its bucket when absent"`
}

// ItemTransitionOutput is item_transition's result.
type ItemTransitionOutput struct {
	Written
	From   string        `json:"from" jsonschema:"the state it left"`
	To     string        `json:"to" jsonschema:"the state it is in"`
	Item   *ItemOutput   `json:"item,omitempty" jsonschema:"the work item, absent for a manual action or one filed into a Part"`
	Part   *PartOutput   `json:"part,omitempty" jsonschema:"the Part the work item was filed into, absent without part"`
	Action *ManualAction `json:"action,omitempty" jsonschema:"the manual action ticked, absent for a work item"`
}

// itemTransition moves one work item through spec §4.3, or ticks one manual action.
func (d Deps) itemTransition(ctx context.Context, in ItemTransitionInput) (ItemTransitionOutput, error) {
	to, err := note.ParseState(in.To)
	if err != nil {
		return ItemTransitionOutput{}, err
	}
	var from note.State
	filedTo := "" // the project of the item filed into a Part
	res, w, err := d.write(ctx, in.Date, "item_transition", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		if it := n.Item(in.Item); it != nil {
			from, _ = note.StateOf(it.Icon)
			if p := projectAt(n, it.Start); p != nil {
				filedTo = p.Repo
			}
		} else if a := n.Action(in.Item); a != nil {
			from = a.State()
		}
		headline := "^" + in.Item + " " + from.String() + " to " + to.String()
		if s := strings.TrimSpace(in.Summary); s != "" {
			headline += ": " + s
		}
		if in.Part != 0 {
			out, err := fileItem(n, src, r, in.Item, to, in.Summary, in.Part)
			return out, headline + partHeadline(in.Part), err
		}
		out, err := note.TransitionItem(n, src, in.Item, to, in.Summary)
		return out, headline, err
	})
	if err != nil {
		return ItemTransitionOutput{}, err
	}
	o := ItemTransitionOutput{Written: w, From: from.String(), To: to.String()}
	if in.Part != 0 {
		p := projectOf(res.Note, filedTo)
		o.Part = &PartOutput{Repo: filedTo, Part: partView(res.Src, partOf(p, in.Part))}
	} else if res.Note.Item(in.Item) != nil {
		io := itemOutput(res.Note, in.Item)
		o.Item = &io
	} else if a := res.Note.Action(in.Item); a != nil {
		o.Action = &ManualAction{Line: a.Line, ID: a.ID, Text: a.Text, Checked: a.Checked}
	} else {
		return ItemTransitionOutput{}, errors.New("the item is gone from the note it was written to")
	}
	return o, nil
}
