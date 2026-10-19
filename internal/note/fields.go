package note

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// The markers of a work item's cost, route and tier. The list form writes
// ` · <cost> · Route: <route> · Tier: <tier>` after its title; the ##### form ends its body with
// the sentences `Cost: <cost>. Route: <route>. Tier: <tier>.`; both put the route and the tier in
// code spans.
const (
	costMark  = "Cost: "
	routeMark = "Route: "
	tierMark  = "Tier: "
)

// fields reads its ref, cost, route and tier from the units the renderer writes (Renderer.title,
// Renderer.Item), and the title they leave, as written. A list item's are read from the end of its
// title line before its sent status and summary: the tier the code span ending it, the route the
// one ending what is left, the cost what follows its first ` · `, which no title holds. A #####
// item's cost, route and tier are its body's last sentences, before its button, dispatch marker
// and `^<id>`; block is its paragraph.
func (p *parser) fields(it *Item, block ast.Node) {
	head, _ := SplitSummary(it.Title)
	if it.Sent != nil {
		head = it.Sent.Title
	}
	if it.Heading {
		end := it.idAt
		if it.Dispatch != nil {
			end = min(end, it.marker.Start)
		}
		if l := p.buttonLink(block); l != nil {
			end = min(end, l.Pos())
		}
		body := strings.TrimRight(string(p.src[block.Lines().At(0).Start:end]), " \t\r\n")
		rest, tier := cutField(body, tierMark, ".")
		rest, route := cutField(rest, routeMark, ".")
		it.Cost, it.Route, it.Tier = lastCost(rest), route, tier
	} else {
		head, it.Tier = cutField(head, fieldSep+tierMark, "")
		head, it.Route = cutField(head, fieldSep+routeMark, "")
		if title, cost, ok := strings.Cut(head, fieldSep); ok && cost != "" {
			head, it.Cost = title, cost
		}
	}
	it.lead, it.Bare = head, head
	if r, rest, ok := titleRef(head); ok {
		it.Ref, it.Bare = r.String(), rest
	}
}

// lastCost reads the cost of body, a ##### item's prose before its route and tier: its last
// sentence, `Cost: <cost>.`, where the mark starts body or follows a space. It is "" without one,
// as for a body ending with a bare `<cost>.`, which reads as prose.
func lastCost(body string) string {
	rest, ok := strings.CutSuffix(body, ".")
	i := strings.LastIndex(rest, costMark)
	if !ok || i < 0 || i > 0 && !isSpace(rune(rest[i-1])) {
		return ""
	}
	if cost := rest[i+len(costMark):]; !strings.Contains(cost, ". ") && !strings.ContainsAny(cost, "\r\n") {
		return cost
	}
	return ""
}

// cutCode reads s as markdown and cuts the field ending it, a route or a tier: the source mark,
// then a code span, then end. A mark that is a word, not one opening with ` · `, must start s or
// follow a space. It returns s before the mark, right-trimmed, and the code span's content; s and
// "" when s ends with none.
func cutField(s, mark, end string) (rest, code string) {
	src := []byte(s)
	para, ok := md.Parser().Parse(text.NewReader(src)).FirstChild().(*ast.Paragraph)
	if !ok {
		return s, ""
	}
	var span *ast.CodeSpan
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		if cs, ok := c.(*ast.CodeSpan); ok {
			span = cs
		}
	}
	if span == nil || span.FirstChild() == nil {
		return s, ""
	}
	first, ok1 := span.FirstChild().(*ast.Text)
	last, ok2 := span.LastChild().(*ast.Text)
	if !ok1 || !ok2 {
		return s, ""
	}
	open, closing := first.Segment.Start-1, last.Segment.Stop // its backticks
	if open < len(mark) || closing >= len(s) || s[open] != '`' || s[closing] != '`' || s[closing+1:] != end ||
		!strings.HasSuffix(s[:open], mark) {
		return s, ""
	}
	at := open - len(mark)
	if !strings.HasPrefix(mark, " ") && at > 0 && !isSpace(rune(s[at-1])) {
		return s, ""
	}
	return strings.TrimRight(s[:at], " \t\r\n"), s[open+1 : closing]
}

// TitleUnit is its title line as a unit, for a write that rewrites only the line: its icon,
// its title as written with its ref link, its cost, route, tier, sent status, summary and dispatch
// marker. Rendered with ItemTitle it gives back the line; its prose, button and prompt are not
// in it.
func (it *Item) TitleUnit() ItemUnit {
	_, summary := SplitSummary(it.Title)
	u := ItemUnit{
		ID: it.ID, Icon: it.Icon, Title: it.lead, Cost: it.Cost, Route: it.Route, Tier: it.Tier, Summary: summary,
		Heading: it.Heading, Dispatch: it.Dispatch,
	}
	if s := it.Sent; s != nil {
		u.Sent = &SentUnit{To: s.To, Status: s.Status, Note: s.Note}
	}
	return u
}
