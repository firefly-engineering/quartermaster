package note

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// This file is the one place the note's line formats live: the formats of the worklog skill's
// template.md and its Buttons section. A unit renders to whole lines ending in "\n", ready to
// splice over a span (ADR 0002). Icons render as given; deriving a heading's icon is not the
// renderer's job.

// Renderer renders the units of one note. Its fields build the ▶ button URIs.
type Renderer struct {
	Vault     string // the Obsidian vault's name, for obsidian:// URIs
	Date      string // the note's date, YYYY-MM-DD
	CommandID string // the Shell commands plugin's id (ShellCommandID); "" leaves every button out
}

// StatusUnit is the status line: `**HH:MM UTC:** <text>` or `**HH:MM UTC, <label>:** <text>`.
type StatusUnit struct {
	Time  string // HH:MM, UTC
	Label string // the phase, "" for none
	Text  string // one line: where the day stands
}

// ObjectiveUnit is one row of the objectives table.
type ObjectiveUnit struct {
	Objective string // the goal and its project, `<objective> (<repo>)`
	DoneWhen  string // the checkable condition
	Icon      Icon   // leads Progress
	Progress  string // after the icon, "" for none
	Refined   string // HH:MM UTC of its last refinement, "" for none; stamped after Objective
	Reason    string // why it was refined; needed with Refined
}

// NextUnit is one action in *Next*.
type NextUnit struct {
	Box    Box    // a checkbox for a manual action, NoBox otherwise
	Action string // what to do
	Cost   string // "" for none
	Ref    string // the work item or anchor id it details, "" for none
	Button string // the ▶ label after the ▶, as `grill in trunk`; "" for none. Needs Ref.
}

// ItemUnit is an entry in a bucket. With an ID it is a work item; without one it is a one-line
// list entry, as `- ⬜ [#M](url): a decision with no prompt yet`.
//
// Its title line reads `<icon> <ref link>: <title> — <summary>`. The list form adds
// ` · <cost> · Route: <route> · Tier: <tier>` before the summary, the route and the tier in code
// spans, and ` ^<id>` after it, so its title holds no ` · `; the `#####` form ends its body with
// `Cost: <cost>. Route: <route>. Tier: <tier>.` instead. A sent status comes before the summary:
// ` → <to>, **<status>**`. The parser reads the ref, cost, route and tier back (Item).
type ItemUnit struct {
	ID      string
	Icon    Icon
	Title   string      // one line, after the icon and ref link; no SummarySep, and in the list form no fieldSep
	Ref     Ref         // the issue or PR it is about, the zero Ref for none
	Repo    string      // the project it is in, where Ref renders as a bare #N; "" for none
	Cost    string      // as `20 min`; "" for none
	Route   string      // the next skill, as `/implement`; "" for none
	Tier    string      // the class of its work, as `design`; "" for none. The renderer takes any word: item_upsert checks it is a tier
	Summary string      // its last transition's summary (spec §4.3), after SummarySep; "" for none
	Heading bool        // the `#####` form; required with a Prompt
	Body    string      // the `#####` form's prose before its button; no blank line
	Button  string      // the ▶ label, as `grill in trunk`; needs a Prompt
	Prompt  *PromptUnit // the collapsed `> [!prompt]-` callout
	Sent    *SentUnit   // in *Sent to other repos*, its sent status, after the title, cost, route and tier; nil for none

	// Dispatch is the dispatch marker written just before its ` ^<id>`: on its title line in
	// the list form, ending its paragraph in the ##### one.
	Dispatch *Dispatch
}

// SummarySep parts a work item's title from its summary. A title holds none, so the first one
// in a title line starts the summary.
const SummarySep = " — "

// NothingLine is the list line of a bucket or Process with nothing in it.
const NothingLine = "- " + string(IconNone) + "\n"

// fieldSep parts a list item's title from its cost, route and tier, and a *Next* line's fields.
const fieldSep = " · "

// DefaultButton is the ▶ label of a prompt sent to the workspace labelled to, when no other
// label is given.
func DefaultButton(to string) string { return "send to " + to }

// Action is a parsed work item's title (Item.Title) as the action of its *Next* line: without
// its summary, or the cost, route and tier the list form puts after ` · `.
func Action(title string) string {
	head, _ := SplitSummary(title)
	head, _, _ = strings.Cut(head, fieldSep)
	return head
}

// SplitSummary parts a parsed work item's title (Item.Title) into the title before its summary
// and the summary, "" when it has none.
func SplitSummary(title string) (head, summary string) {
	head, summary, _ = strings.Cut(title, SummarySep)
	return head, summary
}

// PromptUnit is a work item's herdr-prompt block.
type PromptUnit struct {
	To   string // the project's owner/repo, or a herdr workspace label
	Body string // the prompt, without callout markers
}

// CarryOverUnit is one row of the carry-over table.
type CarryOverUnit struct {
	Item, State, Verdict string
}

// PartUnit is a `### <icon> Part N — <kind>: <title>` section.
type PartUnit struct {
	Icon  Icon
	N     int
	Kind  string // one of PartKinds
	Title string // as `wave 1, **running**`
	Body  string // markdown under the heading, "" for none
}

// BucketUnit is a `#### <icon> <name>` section; with no items it holds `- —`.
type BucketUnit struct {
	Icon  Icon
	Name  string // one of BucketNames
	Items []ItemUnit
}

// ProjectUnit is a `## <icon> <owner/repo>` section.
type ProjectUnit struct {
	Icon    Icon
	Repo    string
	Parts   []PartUnit
	Remains Icon // the `### <icon> What remains` heading's icon
	Buckets []BucketUnit
}

// Day is a whole note, as day_open writes it.
type Day struct {
	Date       string // YYYY-MM-DD
	Previous   string // the previous note's date, "" for none
	Status     StatusUnit
	Objectives []ObjectiveUnit
	Next       []NextUnit
	Projects   []ProjectUnit
	Repos      []string // more owner/repo for the front matter's projects list, after Projects': the carried-over ones
	CarryOver  []CarryOverUnit
	Process    []string // one line each; none renders `- —`
}

// Status renders the status line.
func (r Renderer) Status(u StatusUnit) ([]byte, error) {
	if !isClock(u.Time) {
		return nil, fmt.Errorf("status time %q: want HH:MM", u.Time)
	}
	if err := oneLine("status label", u.Label); err != nil {
		return nil, err
	}
	if strings.ContainsAny(u.Label, "*:") || (u.Label != "" && isBlank(u.Label)) {
		return nil, fmt.Errorf("status label %q: no `*` or `:`, and not blank", u.Label)
	}
	if err := oneLine("status", u.Text); err != nil {
		return nil, err
	}
	head := u.Time + " UTC"
	if u.Label != "" {
		head += ", " + u.Label
	}
	return []byte("**" + head + ":** " + u.Text + "\n"), nil
}

// Objective renders one objectives table row.
func (r Renderer) Objective(u ObjectiveUnit) ([]byte, error) {
	if !u.Icon.isState() {
		return nil, fmt.Errorf("objective %q: progress icon %q is not a status icon", u.Objective, u.Icon)
	}
	progress := string(u.Icon)
	if u.Progress != "" {
		progress += " " + u.Progress
	}
	objective := u.Objective
	if u.Refined != "" || u.Reason != "" {
		if !isClock(u.Refined) {
			return nil, fmt.Errorf("objective %q: refined at %q, want HH:MM", u.Objective, u.Refined)
		}
		if strings.TrimSpace(u.Reason) == "" {
			return nil, fmt.Errorf("objective %q: a refinement needs its reason", u.Objective)
		}
		objective += refinedOpen + u.Refined + refinedMid + u.Reason + ")"
	}
	return tableRow(objective, u.DoneWhen, progress)
}

// An objective's refined stamp: ` (refined HH:MM UTC: <reason>)`.
const (
	refinedOpen = " (refined "
	refinedMid  = " UTC: "
)

// StripRefined is an objective cell without the refined stamp that ends it, if one does.
func StripRefined(cell string) string {
	i := strings.LastIndex(cell, refinedOpen)
	if i < 0 || !strings.HasSuffix(cell, ")") {
		return cell
	}
	clock, _, ok := strings.Cut(cell[i+len(refinedOpen):], refinedMid)
	if !ok || !isClock(clock) {
		return cell
	}
	return cell[:i]
}

// Verdicts are a carry-over row's verdicts (spec §6): keep moves it into its project's bucket,
// defer leaves it for tomorrow's open, drop closes it.
var Verdicts = []string{"keep", "defer", "drop"}

// VerdictCell renders a carry-over row's verdict: `<verdict>` or `<verdict>: <reason>`. A drop
// needs its reason.
func VerdictCell(verdict, reason string) (string, error) {
	if !slices.Contains(Verdicts, verdict) {
		return "", fmt.Errorf("verdict %q: want one of %s", verdict, strings.Join(Verdicts, ", "))
	}
	if err := oneLine("verdict reason", reason); err != nil {
		return "", err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		if verdict == "drop" {
			return "", errors.New("a drop needs its reason")
		}
		return verdict, nil
	}
	return verdict + ": " + reason, nil
}

// ClockLayout is the time layout of the status line's `HH:MM`, in UTC.
const ClockLayout = "15:04"

// Carried is an open item of the previous note, as day_open carries it over (spec §6).
type Carried struct {
	Text      string // the item as written: a title, an objective, an action's text, a row's item
	Repo      string // its project, "" outside one
	ID        string // its `^<id>` there, "" for none; the row links back to it
	Where     string // the bucket or section it is in, "" to leave out
	State     State  // its state, or 0 when StateText holds it
	Manual    bool   // a manual action, which carries a checkbox rather than an icon
	StateText string // the state as written: an objective's progress, a carried row's state
}

// CarriedRow renders c, an open item of the note of date prev, as a carry-over row with no
// verdict: the item `<text> (<repo>) [[<prev>#^<id>|^<id>]]`, and the state
// `<icon> <STATE> in <where>`, `<STATE>, manual action in <where>`, or StateText.
func (r Renderer) CarriedRow(prev string, c Carried) (CarryOverUnit, error) {
	if c.Text == "" {
		return CarryOverUnit{}, errors.New("carried item: empty text")
	}
	if err := oneLine("carried item", c.Text+c.Where+c.StateText); err != nil {
		return CarryOverUnit{}, err
	}
	item := c.Text
	if c.Repo != "" {
		if !isRepo(c.Repo) {
			return CarryOverUnit{}, fmt.Errorf("carried item %q: repo %q: want owner/repo", c.Text, c.Repo)
		}
		item += " (" + c.Repo + ")"
	}
	if c.ID != "" {
		if !ValidID(c.ID) {
			return CarryOverUnit{}, fmt.Errorf("carried item %q: invalid item id ^%s", c.Text, c.ID)
		}
		if !IsDate(prev) {
			return CarryOverUnit{}, fmt.Errorf("carried item %q: previous date %q: want YYYY-MM-DD", c.Text, prev)
		}
		item += " [[" + prev + "#^" + c.ID + "|^" + c.ID + "]]"
	}
	if (c.State == 0) == (c.StateText == "") {
		return CarryOverUnit{}, fmt.Errorf("carried item %q: want one of State and StateText", c.Text)
	}
	if c.StateText != "" {
		return CarryOverUnit{Item: item, State: c.StateText}, nil
	}
	if !c.State.valid() {
		return CarryOverUnit{}, fmt.Errorf("carried item %q: invalid state %d", c.Text, c.State)
	}
	state := string(c.State.Icon()) + " " + c.State.String()
	if c.Manual {
		state = c.State.String() + ", manual action"
	}
	if c.Where != "" {
		state += " in " + c.Where
	}
	return CarryOverUnit{Item: item, State: state}, nil
}

// CarryOver renders one carry-over table row.
func (r Renderer) CarryOver(u CarryOverUnit) ([]byte, error) {
	return tableRow(u.Item, u.State, u.Verdict)
}

// carryOverHeader is the carry-over table's header and delimiter rows.
const carryOverHeader = "| Item | State now | Verdict |\n|---|---|---|\n"

// CarryOverRows renders carry-over table rows, a line each.
func (r Renderer) CarryOverRows(rows []CarryOverUnit) ([]byte, error) {
	var b []byte
	for _, u := range rows {
		line, err := r.CarryOver(u)
		if err != nil {
			return nil, err
		}
		b = append(b, line...)
	}
	return b, nil
}

// CarryOverTable renders the carry-over table: its header, then rows.
func (r Renderer) CarryOverTable(rows []CarryOverUnit) ([]byte, error) {
	b, err := r.CarryOverRows(rows)
	if err != nil {
		return nil, err
	}
	return append([]byte(carryOverHeader), b...), nil
}

// CarryOverSection renders the `## Carry-over` section: its heading, `From [[<prev>]].` when
// prev is not "", and its table holding rows.
func (r Renderer) CarryOverSection(prev string, rows []CarryOverUnit) ([]byte, error) {
	if prev != "" && !IsDate(prev) {
		return nil, fmt.Errorf("previous date %q: want YYYY-MM-DD", prev)
	}
	table, err := r.CarryOverTable(rows)
	if err != nil {
		return nil, err
	}
	s := "## Carry-over\n\n"
	if prev != "" {
		s += "From [[" + prev + "]].\n\n"
	}
	return append([]byte(s), table...), nil
}

// NextLine renders the n-th action of *Next* (1-based):
// `n. [ ] <action> · <cost> · [[#^<id>|details]] · [▶ <label>](<uri>)`.
func (r Renderer) NextLine(n int, u NextUnit) ([]byte, error) {
	if n < 1 {
		return nil, fmt.Errorf("next: line number %d, want 1 or more", n)
	}
	if u.Action == "" {
		return nil, errors.New("next: empty action")
	}
	if err := oneLine("next action", u.Action+u.Cost); err != nil {
		return nil, err
	}
	if u.Ref != "" && !ValidID(u.Ref) {
		return nil, fmt.Errorf("next: invalid item id ^%s: lowercase letters, digits and - only", u.Ref)
	}
	if u.Button != "" && u.Box != NoBox {
		return nil, fmt.Errorf("next %q: a manual action has no button: no session can start it", u.Action)
	}
	if u.Button != "" && u.Ref == "" {
		return nil, fmt.Errorf("next %q: a button needs a Ref to send", u.Action)
	}
	box := map[Box]string{NoBox: "", Unchecked: "[ ] ", Checked: "[x] "}[u.Box]
	parts := []string{box + u.Action}
	if u.Cost != "" {
		parts = append(parts, u.Cost)
	}
	if u.Ref != "" {
		parts = append(parts, "[[#^"+u.Ref+"|details]]")
	}
	button, err := r.button(u.Button, u.Ref)
	if err != nil {
		return nil, err
	}
	if button != "" {
		parts = append(parts, button)
	}
	return []byte(strconv.Itoa(n) + ". " + strings.Join(parts, fieldSep) + "\n"), nil
}

// NextBody renders what follows the `## Next` heading line: nothing for no actions, else a
// blank line and one line per action.
func (r Renderer) NextBody(units []NextUnit) ([]byte, error) {
	if len(units) == 0 {
		return nil, nil
	}
	out := []byte("\n")
	for i, u := range units {
		b, err := r.NextLine(i+1, u)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// Item renders a bucket entry. The list form is one line, `- <icon> <title> ^<id>`. The
// heading form is the `#####` heading, a blank line, its paragraph ending with the button and
// `^<id>`, then a blank line and the collapsed prompt callout when it has a prompt. Without a
// CommandID the button is left out and the prompt kept.
func (r Renderer) Item(u ItemUnit) ([]byte, error) {
	what, title, err := r.title(u)
	if err != nil {
		return nil, err
	}
	if !u.Heading {
		if u.Prompt != nil || u.Body != "" || u.Button != "" {
			return nil, fmt.Errorf("%s: a body, button or prompt needs the ##### form", what)
		}
		return []byte("- " + title), nil
	}
	if u.ID == "" {
		return nil, fmt.Errorf("%s: a ##### work item needs an id", what)
	}
	if u.Button != "" && u.Prompt == nil {
		return nil, fmt.Errorf("%s: a button needs a prompt to send", what)
	}
	var para []string
	if body := strings.TrimSuffix(u.Body, "\n"); body != "" {
		para = strings.Split(body, "\n")
		if slices.ContainsFunc(para, isBlank) {
			return nil, fmt.Errorf("%s: a blank line in the body would end its paragraph", what)
		}
	}
	var tail []string // the body's last sentences: cost, route and tier
	if u.Cost != "" {
		tail = append(tail, costMark+u.Cost+".")
	}
	if u.Route != "" {
		tail = append(tail, routeMark+"`"+u.Route+"`.")
	}
	if u.Tier != "" {
		tail = append(tail, tierMark+"`"+u.Tier+"`.")
	}
	switch {
	case len(tail) == 0:
	case len(para) == 0:
		para = []string{strings.Join(tail, " ")}
	default:
		para[len(para)-1] += " " + strings.Join(tail, " ")
	}
	button, err := r.button(u.Button, u.ID)
	if err != nil {
		return nil, err
	}
	if button != "" {
		para = append(para, button)
	}
	if u.Dispatch != nil {
		if len(para) == 0 {
			para = []string{r.DispatchMarker(*u.Dispatch)}
		} else {
			para[len(para)-1] += " " + r.DispatchMarker(*u.Dispatch)
		}
	}
	if len(para) == 0 {
		para = []string{"^" + u.ID}
	} else {
		para[len(para)-1] += " ^" + u.ID
	}
	var b strings.Builder
	b.WriteString("##### " + title + "\n")
	b.WriteString(strings.Join(para, "\n") + "\n")
	if u.Prompt != nil {
		callout, err := prompt(u.ID, *u.Prompt)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		b.WriteString("\n" + callout)
	}
	return []byte(b.String()), nil
}

// ItemTitle renders a work item's title line from its icon on: the bytes a transition writes
// over an item's Head from its IconSpan, as `✅ <title> — <summary> ^<id>` for the list form and
// `✅ <title> — <summary>` for the ##### one. Its prose, button and prompt are not rendered.
func (r Renderer) ItemTitle(u ItemUnit) ([]byte, error) {
	_, title, err := r.title(u)
	return []byte(title), err
}

// title checks u and renders its title line from its icon on, with its line end; what names u
// in errors.
func (r Renderer) title(u ItemUnit) (what, title string, err error) {
	what = "entry " + strconv.Quote(u.Title)
	if u.ID != "" {
		what = "work item ^" + u.ID
		if !ValidID(u.ID) {
			return "", "", fmt.Errorf("invalid item id ^%s: lowercase letters, digits and - only", u.ID)
		}
	}
	if !u.Icon.isState() {
		return "", "", fmt.Errorf("%s: icon %q is not a status icon", what, u.Icon)
	}
	if u.Title == "" {
		return "", "", fmt.Errorf("%s: empty title", what)
	}
	var sent string
	if u.Sent != nil {
		if sent, err = sentTail(*u.Sent); err != nil {
			return "", "", fmt.Errorf("%s: %w", what, err)
		}
		if parseSent(u.Title) != nil {
			return "", "", fmt.Errorf("%s: the title already ends with a sent status; give it as the sent status alone", what)
		}
	}
	if err := oneLine(what+" title", u.Title+u.Cost+u.Route+u.Tier+u.Summary+sent); err != nil {
		return "", "", err
	}
	if w := strings.Fields(u.Title); strings.HasPrefix(w[len(w)-1], "^") {
		return "", "", fmt.Errorf("%s: the title ends in a block id; set ID instead", what)
	}
	if strings.Contains(u.Title+u.Cost+u.Route+u.Tier+sent, SummarySep) {
		return "", "", fmt.Errorf("%s: %q starts the summary, so the title, cost, route and tier may not hold it", what, SummarySep)
	}
	if u.Dispatch != nil {
		if err := u.Dispatch.Check(); err != nil {
			return "", "", fmt.Errorf("%s: %w", what, err)
		}
		if u.ID == "" {
			return "", "", fmt.Errorf("%s: a dispatch marker needs an id", what)
		}
	}
	if !u.Heading && strings.Contains(u.Title, fieldSep) {
		return "", "", fmt.Errorf("%s: %q parts a one-line title from its cost, route and tier, so the title may not hold it", what, fieldSep)
	}
	if strings.Contains(u.Route, "`") {
		return "", "", fmt.Errorf("%s: route %q: no backticks, it renders in a code span", what, u.Route)
	}
	if strings.Contains(u.Tier, "`") {
		return "", "", fmt.Errorf("%s: tier %q: no backticks, it renders in a code span", what, u.Tier)
	}
	title = string(u.Icon) + " "
	if u.Ref != (Ref{}) {
		link, err := refLink(u.Ref, u.Repo)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", what, err)
		}
		title += link + ": "
	}
	title += u.Title
	if !u.Heading {
		if u.Cost != "" {
			title += fieldSep + u.Cost
		}
		if u.Route != "" {
			title += fieldSep + routeMark + "`" + u.Route + "`"
		}
		if u.Tier != "" {
			title += fieldSep + tierMark + "`" + u.Tier + "`"
		}
	}
	title += sent
	if u.Summary != "" {
		title += SummarySep + u.Summary
	}
	if !u.Heading && u.ID != "" {
		if u.Dispatch != nil {
			title += " " + r.DispatchMarker(*u.Dispatch)
		}
		title += " ^" + u.ID
	}
	return what, title + "\n", nil
}

// refLink renders a ref as a full link (ADR 0007): `[#N](<url>)` in its own project, and
// `[<repo>#N](<url>)` in another, as the skill writes `[IaC#564](…)`.
func refLink(f Ref, project string) (string, error) {
	_, repo, _ := strings.Cut(f.Repo, "/")
	if !isRepo(f.Repo) || f.N < 1 {
		return "", fmt.Errorf("ref %+v: want owner/repo#N", f)
	}
	label := "#" + strconv.Itoa(f.N)
	if f.Repo != project {
		label = repo + label
	}
	return "[" + label + "](" + f.URL() + ")", nil
}

// Note renders a whole note from d, in template.md's layout. Its buttons carry d.Date, the
// note's own date, whatever r.Date holds.
func (r Renderer) Note(d Day) ([]byte, error) {
	if !IsDate(d.Date) {
		return nil, fmt.Errorf("note date %q: want YYYY-MM-DD", d.Date)
	}
	r.Date = d.Date
	if d.Previous != "" && !IsDate(d.Previous) {
		return nil, fmt.Errorf("previous date %q: want YYYY-MM-DD", d.Previous)
	}
	var blocks []string
	add := func(b ...string) { blocks = append(blocks, b...) }

	fm := "---\ndate: " + d.Date + "\n"
	if d.Previous != "" {
		fm += "previous: \"[[" + d.Previous + "]]\"\n"
	}
	var repos []string
	for _, p := range d.Projects {
		repos = append(repos, p.Repo)
	}
	var listed []string
	for _, repo := range append(repos, d.Repos...) {
		if !isRepo(repo) {
			return nil, fmt.Errorf("project repo %q: want owner/repo", repo)
		}
		if !slices.Contains(listed, repo) {
			listed = append(listed, repo)
		}
	}
	if len(listed) == 0 {
		fm += projectsKey + ": []\n"
	} else {
		fm += projectsKey + ":\n"
	}
	for _, repo := range listed {
		fm += projectEntry(repo, "\n")
	}
	add(fm+"tags:\n  - worklog\n---\n", "# Worklog — "+d.Date+"\n")

	status, err := r.Status(d.Status)
	if err != nil {
		return nil, err
	}
	add(string(status))

	objectives := "| Objective | Done when | Progress |\n|---|---|---|\n"
	for _, o := range d.Objectives {
		b, err := r.Objective(o)
		if err != nil {
			return nil, err
		}
		objectives += string(b)
	}
	add("## Objectives\n", objectives)

	next, err := r.NextBody(d.Next)
	if err != nil {
		return nil, err
	}
	add("## Next\n" + string(next)) // the body brings its own blank line

	for _, p := range d.Projects {
		b, err := r.project(p)
		if err != nil {
			return nil, err
		}
		add(b...)
	}

	carry, err := r.CarryOverSection(d.Previous, d.CarryOver)
	if err != nil {
		return nil, err
	}
	add(string(carry))

	process, err := list(d.Process, "process")
	if err != nil {
		return nil, err
	}
	add("## Process\n", process)
	return []byte(strings.Join(blocks, "\n")), nil
}

// project renders a project section as blocks, to be joined by blank lines.
func (r Renderer) project(p ProjectUnit) ([]string, error) {
	if !p.Icon.isHeading() {
		return nil, fmt.Errorf("project %s: icon %q", p.Repo, p.Icon)
	}
	blocks := []string{"## " + string(p.Icon) + " " + p.Repo + "\n"}
	for _, pt := range p.Parts {
		b, err := part(pt)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", p.Repo, err)
		}
		blocks = append(blocks, b...)
	}
	if len(p.Buckets) == 0 {
		return blocks, nil
	}
	remains, err := r.remains(p.Remains, p.Repo, p.Buckets)
	if err != nil {
		return nil, fmt.Errorf("project %s: %w", p.Repo, err)
	}
	return append(blocks, remains...), nil
}

// part renders a Part as blocks: its heading, then its body when it has one.
func part(pt PartUnit) ([]string, error) {
	if !pt.Icon.isHeading() || pt.N < 1 || !slices.Contains(PartKinds, pt.Kind) {
		return nil, fmt.Errorf("Part %d: want a heading icon, N ≥ 1 and a kind in %s, got %q %q",
			pt.N, strings.Join(PartKinds, ", "), pt.Icon, pt.Kind)
	}
	if err := oneLine("Part title", pt.Title); err != nil {
		return nil, err
	}
	head := "### " + string(pt.Icon) + " Part " + strconv.Itoa(pt.N) + " — " + pt.Kind
	if pt.Title != "" {
		head += ": " + pt.Title
	}
	blocks := []string{head + "\n"}
	body := strings.Trim(pt.Body, "\n")
	if body == "" {
		return blocks, nil
	}
	doc := md.Parser().Parse(text.NewReader([]byte(body)))
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(*ast.Heading); ok {
			return nil, fmt.Errorf("Part %d: its body may hold no heading, which would end the Part", pt.N)
		}
	}
	return append(blocks, body+"\n"), nil
}

// Part renders a Part whole, heading and body, to splice over one or into its project.
func (r Renderer) Part(pt PartUnit) ([]byte, error) {
	blocks, err := part(pt)
	return []byte(strings.Join(blocks, "\n")), err
}

// ActionUnit is a manual action outside *Next*: `- [ ] <text> ^<id>`.
type ActionUnit struct {
	ID      string
	Text    string // one line, after the box
	Checked bool
}

// ActionLine renders a manual action's list line.
func (r Renderer) ActionLine(u ActionUnit) ([]byte, error) {
	box := "[ ]"
	if u.Checked {
		box = "[x]"
	}
	rest, err := actionText(u)
	return []byte("- " + box + rest), err
}

// actionText renders what follows a manual action's box: ` <text> ^<id>` and its line end.
func actionText(u ActionUnit) (string, error) {
	if !ValidID(u.ID) {
		return "", fmt.Errorf("invalid manual action id ^%s: lowercase letters, digits and - only", u.ID)
	}
	what := "manual action ^" + u.ID
	text := strings.TrimSpace(u.Text)
	if text == "" {
		return "", fmt.Errorf("%s: empty text", what)
	}
	if err := oneLine(what, text); err != nil {
		return "", err
	}
	if lead, _, _ := strings.Cut(text, " "); Icon(lead).isHeading() {
		return "", fmt.Errorf("%s: a manual action carries a checkbox, never a status icon", what)
	}
	if w := strings.Fields(text); strings.HasPrefix(w[len(w)-1], "^") {
		return "", fmt.Errorf("%s: the text ends in a block id; set ID instead", what)
	}
	return " " + text + " ^" + u.ID + "\n", nil
}

// RewordAction rewrites manual action a's text after its box, keeping its marker, indent, box
// and nested lines: a reword never ticks or unticks it.
func RewordAction(src []byte, a ManualAction, text string) ([]byte, error) {
	rest, err := actionText(ActionUnit{ID: a.ID, Text: text})
	if err != nil {
		return nil, err
	}
	return Replace(src, Span{Start: a.Mark + 2, End: a.Head.End}, []byte(rest)), nil // past the box's `]`
}

// ProcessSection renders a `## Process` section holding list, its lines, to add to a note that
// lacks one.
func (r Renderer) ProcessSection(list []byte) []byte {
	return []byte("## " + ProcessSection + "\n\n" + string(list))
}

// ProcessLine renders one *Process* entry's list line: `- <text>`.
func (r Renderer) ProcessLine(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == string(IconNone) {
		return nil, fmt.Errorf("process entry %q: want some text", text)
	}
	if err := oneLine("process entry", text); err != nil {
		return nil, err
	}
	if strings.HasPrefix(text, "[ ]") || strings.HasPrefix(text, "[x]") || strings.HasPrefix(text, "[X]") {
		return nil, fmt.Errorf("process entry %q: a checkbox is a manual action: action_upsert writes one", text)
	}
	if w := strings.Fields(text); strings.HasPrefix(w[len(w)-1], "^") {
		return nil, fmt.Errorf("process entry %q: an entry has no block id", text)
	}
	return []byte("- " + text + "\n"), nil
}

// Project renders a project section, to splice into a note that lacks it.
func (r Renderer) Project(p ProjectUnit) ([]byte, error) {
	blocks, err := r.project(p)
	return []byte(strings.Join(blocks, "\n")), err
}

// Remains renders a `### <icon> What remains` section, to splice into a project that lacks it.
func (r Renderer) Remains(icon Icon, buckets []BucketUnit) ([]byte, error) {
	blocks, err := r.remains(icon, "", buckets)
	return []byte(strings.Join(blocks, "\n")), err
}

// Bucket renders a `#### <icon> <name>` section, to splice into a *What remains* that lacks it.
func (r Renderer) Bucket(b BucketUnit) ([]byte, error) {
	blocks, err := r.bucket(b, "")
	return []byte(strings.Join(blocks, "\n")), err
}

// remains renders a What remains section of project repo as blocks.
func (r Renderer) remains(icon Icon, repo string, buckets []BucketUnit) ([]string, error) {
	if !icon.isHeading() {
		return nil, fmt.Errorf("the What remains icon %q is not a heading icon", icon)
	}
	blocks := []string{"### " + string(icon) + " What remains\n"}
	for _, b := range buckets {
		bb, err := r.bucket(b, repo)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, bb...)
	}
	return blocks, nil
}

// bucket renders a bucket of project repo as blocks; an item's ref there renders as #N.
func (r Renderer) bucket(b BucketUnit, repo string) ([]string, error) {
	if !slices.Contains(BucketNames, b.Name) {
		return nil, fmt.Errorf("unknown bucket %q: want one of %s", b.Name, strings.Join(BucketNames, ", "))
	}
	if !b.Icon.isHeading() {
		return nil, fmt.Errorf("bucket %s: icon %q", b.Name, b.Icon)
	}
	blocks := []string{"#### " + string(b.Icon) + " " + b.Name + "\n"}
	if len(b.Items) == 0 {
		blocks = append(blocks, NothingLine)
	}
	// Consecutive list entries form one list; a ##### item is a block of its own.
	var run string
	for _, it := range b.Items {
		if it.Repo == "" {
			it.Repo = repo
		}
		unit, err := r.Item(it)
		if err != nil {
			return nil, err
		}
		if !it.Heading {
			run += string(unit)
			continue
		}
		if run != "" {
			blocks, run = append(blocks, run), ""
		}
		blocks = append(blocks, string(unit))
	}
	if run != "" {
		blocks = append(blocks, run)
	}
	return blocks, nil
}

// button renders `[▶ <label>](obsidian://shell-commands/?…)` for item id, or "" when there is
// no label or no command id.
func (r Renderer) button(label, id string) (string, error) {
	if label == "" || r.CommandID == "" {
		return "", nil
	}
	if strings.ContainsAny(label, "[]\n") {
		return "", fmt.Errorf("button label %q: no brackets or newlines", label)
	}
	if r.Vault == "" {
		return "", errors.New("button: no vault name")
	}
	if !IsDate(r.Date) {
		return "", fmt.Errorf("button: note date %q: want YYYY-MM-DD", r.Date)
	}
	uri := buttonURI + "?vault=" + queryValue(r.Vault) +
		"&execute=" + queryValue(r.CommandID) +
		"&_date=" + r.Date + "&_item=" + id
	return "[" + buttonMark + label + "](" + uri + ")", nil
}

// A button is a link to buttonURI whose text is buttonMark and its label.
const (
	buttonURI  = "obsidian://shell-commands/"
	buttonMark = "▶ "
)

// prompt renders the collapsed callout holding item id's herdr-prompt block.
func prompt(id string, p PromptUnit) (string, error) {
	if p.To == "" || strings.ContainsFunc(p.To, isSpace) {
		return "", fmt.Errorf("prompt to=%q: want owner/repo or a workspace label, with no spaces", p.To)
	}
	body := strings.TrimSuffix(p.Body, "\n")
	if strings.TrimSpace(body) == "" {
		return "", errors.New("empty prompt")
	}
	var b strings.Builder
	b.WriteString("> [!prompt]- Prompt sent to " + p.To + "\n")
	b.WriteString("> ```herdr-prompt id=" + id + " to=" + p.To + "\n")
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			return "", fmt.Errorf("prompt line %q would close its ``` fence", line)
		}
		if line == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> " + line + "\n")
		}
	}
	b.WriteString("> ```\n")
	return b.String(), nil
}

// tableRow renders a table row, each cell as Cell writes it.
func tableRow(cells ...string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("|")
	for _, c := range cells {
		if err := oneLine("table cell", c); err != nil {
			return nil, err
		}
		b.WriteString(" " + Cell(c) + " |")
	}
	return []byte(b.String() + "\n"), nil
}

// Cell is c as a table cell holds it: a bare `|` is escaped so it stays in its cell. A `\|`
// is kept, so Cell of a parsed cell is that cell.
func Cell(c string) string {
	var b strings.Builder
	escaped := false // the byte before is an unescaped backslash
	for i := range len(c) {
		if c[i] == '|' && !escaped {
			b.WriteByte('\\')
		}
		escaped = c[i] == '\\' && !escaped
		b.WriteByte(c[i])
	}
	return b.String()
}

// list renders lines as a `- ` list, `- —` when there are none.
func list(lines []string, what string) (string, error) {
	if len(lines) == 0 {
		return NothingLine, nil
	}
	var b strings.Builder
	for _, l := range lines {
		if err := oneLine(what, l); err != nil {
			return "", err
		}
		b.WriteString("- " + l + "\n")
	}
	return b.String(), nil
}

// queryValue percent-encodes a URI query value, a space as %20 rather than +.
func queryValue(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func oneLine(what, s string) error {
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%s %q: want one line", what, s)
	}
	return nil
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func isClock(s string) bool {
	_, err := time.Parse(ClockLayout, s)
	return err == nil && len(s) == 5
}

// IsDate reports whether s is a note's date, YYYY-MM-DD, written as Go writes it.
func IsDate(s string) bool {
	t, err := time.Parse(time.DateOnly, s)
	return err == nil && t.Format(time.DateOnly) == s
}
