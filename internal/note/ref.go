package note

import (
	"fmt"
	"strconv"
	"strings"
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
