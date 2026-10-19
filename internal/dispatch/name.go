package dispatch

import (
	"path/filepath"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/send"
)

// wsPrefix starts the name of a fresh dispatch's workspace for issue N: issue-<N>. A batch's
// workspace is named after its item id, which never starts with it.
const wsPrefix = "issue-"

// IsFreshAgent reports whether name is one a fresh dispatch gives the agent it starts in
// workspace ws, whatever its counter: `<repo>-<N>` in `<workspace_root>/<owner>/<repo>/issue-<N>`,
// or a batch's `<repo>-<item-id>` in `<workspace_root>/<owner>/<repo>/<item-id>`. An agent
// renamed since, or a session's, is not.
func IsFreshAgent(name, ws string) bool {
	_, repo, wsName := SplitCheckout(ws)
	return send.IsAgentName(name, repo, strings.TrimPrefix(wsName, wsPrefix))
}

// checkoutPath is the path of a fresh dispatch's checkout named name, of repo, owner/repo:
// `<root>/<owner>/<repo>/<name>`. SplitCheckout takes it apart.
func checkoutPath(root, repo, name string) string {
	owner, repo, _ := strings.Cut(repo, "/")
	return filepath.Join(root, owner, repo, name)
}

// SplitCheckout is the owner, repo and name of ws, a fresh dispatch's checkout,
// `<workspace_root>/<owner>/<repo>/<name>`.
func SplitCheckout(ws string) (owner, repo, name string) {
	dir := filepath.Dir(ws)
	return filepath.Base(filepath.Dir(dir)), filepath.Base(dir), filepath.Base(ws)
}
