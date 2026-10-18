// Package skills embeds the worklog skill, so the MCP workflow_rules prompt serves the same
// text the skill bundle installs (ADR 0005).
package skills

import "embed"

//go:embed worklog/SKILL.md worklog/template.md
var Worklog embed.FS
