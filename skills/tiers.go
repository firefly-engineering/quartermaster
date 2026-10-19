package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// tiersHeading is the level-2 heading of the worklog skill whose first table lists the tiers.
const tiersHeading = "Tiers"

// Tiers returns the closed list of work item tiers, in the skill's order: the code span in each
// row's Tier cell of the table under the worklog skill's `## Tiers` (ADR 0011). The skill owns
// the list, so the binary holds no copy of it (ADR 0005).
var Tiers = sync.OnceValues(func() ([]string, error) {
	src, err := fs.ReadFile(Worklog, "worklog/SKILL.md")
	if err != nil {
		return nil, err
	}
	tiers, err := cutTiers(src)
	if err != nil {
		return nil, fmt.Errorf("worklog/SKILL.md: %w", err)
	}
	return tiers, nil
})

// cutTiers reads the tiers from the first table after the `## Tiers` heading, before the next
// heading of level 2 or less.
func cutTiers(src []byte) ([]string, error) {
	doc := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(src))
	in := false
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level <= 2 {
			if in {
				break
			}
			in = h.Level == 2 && nodeText(h, src) == tiersHeading
			continue
		}
		if t, ok := n.(*east.Table); ok && in {
			return tableTiers(t, src)
		}
	}
	return nil, errors.New("no table under \"## " + tiersHeading + "\"")
}

// tableTiers reads each row's code span in the column headed Tier.
func tableTiers(t *east.Table, src []byte) ([]string, error) {
	col := -1
	var tiers []string
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		i := 0
		for c := row.FirstChild(); c != nil; c, i = c.NextSibling(), i+1 {
			if row.Kind() == east.KindTableRow && c.NextSibling() == nil && i < col {
				return nil, errors.New("a tiers table row has no Tier cell")
			}
			switch {
			case row.Kind() == east.KindTableHeader && nodeText(c, src) == "Tier":
				col = i
			case row.Kind() == east.KindTableRow && i == col:
				code, ok := c.FirstChild().(*ast.CodeSpan)
				if !ok || code.NextSibling() != nil || nodeText(code, src) == "" {
					return nil, fmt.Errorf("tier %q is not one code span", nodeText(c, src))
				}
				tier := nodeText(code, src)
				if slices.Contains(tiers, tier) {
					return nil, fmt.Errorf("two tiers named %q", tier)
				}
				tiers = append(tiers, tier)
			}
		}
		if col < 0 {
			return nil, errors.New("the tiers table has no Tier column")
		}
	}
	if len(tiers) == 0 {
		return nil, errors.New("the tiers table has no rows")
	}
	return tiers, nil
}

// nodeText is n's inline text, without markup.
func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := n.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}
