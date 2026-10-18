package note

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Ref is a GitHub issue or PR, `owner/repo#N`, unambiguous across projects (ADR 0007). The zero
// Ref is no ref.
type Ref struct {
	Repo string // owner/repo
	N    int
}

// ParseRef reads `owner/repo#N`, or a bare `#N` in project, the `owner/repo` in scope; project
// "" puts none in scope.
func ParseRef(s, project string) (Ref, error) {
	repo, num, ok := strings.Cut(s, "#")
	if !ok {
		return Ref{}, fmt.Errorf("ref %q: want owner/repo#N", s)
	}
	if repo == "" {
		if project == "" {
			return Ref{}, fmt.Errorf("ref %q: no project in scope, want owner/repo#N", s)
		}
		repo = project
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 || strconv.Itoa(n) != num || !isRepo(repo) {
		return Ref{}, fmt.Errorf("ref %q: want owner/repo#N", s)
	}
	return Ref{Repo: repo, N: n}, nil
}

// String is the ref as `owner/repo#N`.
func (f Ref) String() string { return f.Repo + "#" + strconv.Itoa(f.N) }

// URL is the ref's issue page on GitHub, which GitHub redirects to a PR's.
func (f Ref) URL() string { return "https://github.com/" + f.Repo + "/issues/" + strconv.Itoa(f.N) }

// ItemsByRef returns the work items whose title opens with a link to r, an issue or a PR on
// GitHub, in note order. The link's label is not read: `[#N]`, `[repo#N]` and any other name
// it.
func (n *Note) ItemsByRef(r Ref) []*Item {
	var out []*Item
	for _, p := range n.Projects {
		for _, pt := range p.Parts {
			out = appendByRef(out, pt.Items, r)
		}
		if p.Remains != nil {
			for _, b := range p.Remains.Buckets {
				out = appendByRef(out, b.Items, r)
			}
		}
	}
	slices.SortFunc(out, func(a, b *Item) int { return a.Start - b.Start })
	return out
}

func appendByRef(out, items []*Item, r Ref) []*Item {
	for _, it := range items {
		if f, ok := titleRef(it.Title); ok && f == r {
			out = append(out, it)
		}
	}
	return out
}

// Ref is the GitHub issue or PR the link opening its title names, before its summary; ok is
// false when its title opens with no such link.
func (it *Item) Ref() (Ref, bool) {
	head, _ := SplitSummary(it.Title)
	return titleRef(head)
}

// titleRef reads the ref of the link opening title, as the renderer writes it (refLink).
func titleRef(title string) (Ref, bool) {
	src := []byte(title)
	doc := md.Parser().Parse(text.NewReader(src))
	para := doc.FirstChild()
	if para == nil {
		return Ref{}, false
	}
	l, ok := para.FirstChild().(*ast.Link)
	if !ok {
		return Ref{}, false
	}
	u, err := url.Parse(string(l.Destination))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return Ref{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "issues" && parts[2] != "pull" {
		return Ref{}, false
	}
	f, err := ParseRef(parts[0]+"/"+parts[1]+"#"+parts[3], "")
	return f, err == nil
}
