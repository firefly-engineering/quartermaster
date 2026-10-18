package note

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// The markers of a work item's cost and route. The list form writes ` · <cost> · Route: <route>`
// after its title; the ##### form ends its body with the sentences `Cost: <cost>. Route: <route>.`;
// both put the route in a code span.
const (
	costMark  = "Cost: "
	routeMark = "Route: "
)

// fields reads its ref, cost and route from the units the renderer writes (Renderer.title,
// Renderer.Item), and the title they leave, as written. A list item's are read from its title
// line before its sent status and summary: the route the code span ending it, the cost what
// follows its first ` · `, which no title holds. A ##### item's cost and route are its body's
// last sentences, before its button, dispatch marker and `^<id>`; block is its paragraph.
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
		rest, route := cutRoute(body, routeMark, ".")
		it.Cost, it.Route = lastCost(rest), route
	} else {
		var route string
		head, route = cutRoute(head, fieldSep+routeMark, "")
		if title, cost, ok := strings.Cut(head, fieldSep); ok && cost != "" {
			head, it.Cost = title, cost
		}
		it.Route = route
	}
	it.lead = head
	if r, ok := titleRef(head); ok {
		it.Ref = r.String()
	}
}

// lastCost reads the cost of body, a ##### item's prose before its route: its last sentence,
// `Cost: <cost>.`, where the mark starts body or follows a space. It is "" without one, as for a
// body ending with a bare `<cost>.`, which reads as prose.
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

// cutRoute reads s as markdown and cuts the route ending it: the source mark, then a code span,
// then end. A mark that is a word, not one opening with ` · `, must start s or follow a space.
// It returns s before the mark, right-trimmed, and the route; s and "" when s ends with none.
func cutRoute(s, mark, end string) (rest, route string) {
	src := []byte(s)
	para, ok := md.Parser().Parse(text.NewReader(src)).FirstChild().(*ast.Paragraph)
	if !ok {
		return s, ""
	}
	var code *ast.CodeSpan
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		if cs, ok := c.(*ast.CodeSpan); ok {
			code = cs
		}
	}
	if code == nil || code.FirstChild() == nil {
		return s, ""
	}
	first, ok1 := code.FirstChild().(*ast.Text)
	last, ok2 := code.LastChild().(*ast.Text)
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
// its title as written with its ref link, its cost, route, sent status, summary and dispatch
// marker. Rendered with ItemTitle it gives back the line; its prose, button and prompt are not
// in it.
func (it *Item) TitleUnit() ItemUnit {
	_, summary := SplitSummary(it.Title)
	u := ItemUnit{
		ID: it.ID, Icon: it.Icon, Title: it.lead, Cost: it.Cost, Route: it.Route, Summary: summary,
		Heading: it.Heading, Dispatch: it.Dispatch,
	}
	if s := it.Sent; s != nil {
		u.Sent = &SentUnit{To: s.To, Status: s.Status, Note: s.Note}
	}
	return u
}
