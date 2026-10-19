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
	suffix := strings.TrimPrefix(filepath.Base(ws), wsPrefix)
	return send.IsAgentName(name, filepath.Base(filepath.Dir(ws)), suffix)
}
