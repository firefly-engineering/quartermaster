package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// cycleHeading is the level-2 heading whose level-3 subsections are the phases.
const cycleHeading = "The day's cycle"

// cutPhases returns the skill's phases, keyed by lowercased heading text. Each value is
// the source of one level-3 section under the cycle heading, from its heading line up to
// the next heading of level 3 or less (ADR 0002: splice by source range, never re-render).
func cutPhases(src []byte) (map[string]string, error) {
	doc := goldmark.DefaultParser().Parse(text.NewReader(src))
	var (
		phases  = map[string]string{}
		inCycle bool
		name    string
		start   = -1
	)
	closeSection := func(end int) {
		if start >= 0 {
			phases[name] = string(src[start:end])
			start = -1
		}
	}
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Level > 3 {
			continue
		}
		if h.Pos() < 0 {
			return nil, fmt.Errorf("heading %q has no source position", headingText(h, src))
		}
		closeSection(h.Pos())
		switch {
		case h.Level < 3:
			if inCycle {
				return phases, nil
			}
			inCycle = h.Level == 2 && headingText(h, src) == cycleHeading
		case inCycle:
			name, start = strings.ToLower(headingText(h, src)), h.Pos()
			if _, dup := phases[name]; dup {
				return nil, fmt.Errorf("two phases named %q", name)
			}
		}
	}
	if !inCycle {
		return nil, errors.New("skill has no \"## " + cycleHeading + "\" section")
	}
	closeSection(len(src))
	return phases, nil
}

// headingText is the heading's inline text, without markup.
func headingText(h *ast.Heading, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(h, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := n.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}
