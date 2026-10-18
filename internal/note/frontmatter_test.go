package note

import (
	"strings"
	"testing"
)

// TestListProject adds a repo to the front matter's projects list by splicing the list as
// written, and leaves every other byte alone. A note with no front matter is left as it is: a
// note not written from the template gains none. One with no projects key gains it, before
// the closing fence.
func TestListProject(t *testing.T) {
	t.Parallel()
	const title = "# Worklog — 2026-10-18\n"
	cases := []struct{ name, fm, want string }{
		{"the template's block list",
			"---\ndate: 2026-10-18\nprojects:\n  - acme/web\ntags:\n  - worklog\n---\n",
			"---\ndate: 2026-10-18\nprojects:\n  - acme/web\n  - acme/api\ntags:\n  - worklog\n---\n"},
		{"CRLF line ends",
			"---\r\ndate: 2026-10-18\r\nprojects:\r\n  - acme/web\r\ntags:\r\n  - worklog\r\n---\r\n",
			"---\r\ndate: 2026-10-18\r\nprojects:\r\n  - acme/web\r\n  - acme/api\r\ntags:\r\n  - worklog\r\n---\r\n"},
		{"the list's own indent and comment",
			"---\nprojects:\n- acme/web # the site\ntags: [worklog]\n---\n",
			"---\nprojects:\n- acme/web # the site\n- acme/api\ntags: [worklog]\n---\n"},
		{"listed already, quoted",
			"---\nprojects:\n  - \"acme/api\"\n---\n",
			"---\nprojects:\n  - \"acme/api\"\n---\n"},
		{"day_open's empty list",
			"---\ndate: 2026-10-18\nprojects: []\ntags:\n  - worklog\n---\n",
			"---\ndate: 2026-10-18\nprojects:\n  - acme/api\ntags:\n  - worklog\n---\n"},
		{"an empty list, CRLF",
			"---\r\nprojects:  [ ]\r\n---\r\n",
			"---\r\nprojects:\r\n  - acme/api\r\n---\r\n"},
		{"a flow list",
			"---\nprojects: [acme/web, acme/infra] # two\n---\n",
			"---\nprojects: [acme/web, acme/infra, acme/api] # two\n---\n"},
		{"a key with no value",
			"---\nprojects:\ntags: []\n---\n",
			"---\nprojects:\n  - acme/api\ntags: []\n---\n"},
		{"columns in characters", // yaml counts é as one column
			"---\nprojects: [café/x]\n---\n",
			"---\nprojects: [café/x, acme/api]\n---\n"},
		{"lines by yaml's breaks", // yaml ends a line at U+2028 too
			"---\n# a\u2028# b\nprojects: [x/y]\n---\n",
			"---\n# a\u2028# b\nprojects: [x/y, acme/api]\n---\n"},
		{"a key with no value, CRLF",
			"---\r\nprojects: # none yet\r\n---\r\n",
			"---\r\nprojects: # none yet\r\n  - acme/api\r\n---\r\n"},
		{"no projects key, CRLF",
			"---\r\ndate: 2026-10-18\r\n---\r\n",
			"---\r\ndate: 2026-10-18\r\nprojects:\r\n  - acme/api\r\n---\r\n"},
		{"no projects key",
			"---\ndate: 2026-10-18\n---\n",
			"---\ndate: 2026-10-18\nprojects:\n  - acme/api\n---\n"},
		{"empty front matter, CRLF",
			"---\r\n---\r\n",
			"---\r\nprojects:\r\n  - acme/api\r\n---\r\n"},
		{"no front matter", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := []byte(c.fm + title)
			n, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ListProject(src, n.FrontMatter, "acme/api")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want+title {
				t.Errorf("got:\n%q\nwant:\n%q", got, c.want+title)
			}
		})
	}
}

func TestListProjectRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, fm, repo, want string }{
		{"not owner/repo", "---\nprojects: []\n---\n", "acme", "owner/repo"},
		{"not YAML", "---\nprojects: [\n---\n", "acme/api", "front matter"},
		{"not a mapping", "---\n- acme/web\n---\n", "acme/api", "mapping"},
		{"a flow mapping", "---\n{date: 2026-10-18}\n---\n", "acme/api", "block mapping"},
		{"projects not a list", "---\nprojects: acme/web\n---\n", "acme/api", "not a list"},
		{"a quoted flow item", "---\nprojects: [\"acme/web\"]\n---\n", "acme/api", "by hand"},
		{"a block item over two lines", "---\nprojects:\n  - acme/web\n    more\n---\n", "acme/api", "by hand"},
		{"a nested block item", "---\nprojects:\n  - name: acme/web\n---\n", "acme/api", "by hand"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := []byte(c.fm + "# Worklog — 2026-10-18\n")
			n, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ListProject(src, n.FrontMatter, c.repo); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want an error naming %q", err, c.want)
			}
		})
	}
}
