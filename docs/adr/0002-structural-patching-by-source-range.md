# Mutations splice source ranges located by a markdown parser

The server parses the note with goldmark. A mutation locates each unit it changes (section,
table row, list item, work item, fence) by the unit's source byte range, renders only that
unit, and splices the new bytes into the original source. Nothing is re-rendered from an
AST.

## Why

goldmark has no faithful markdown renderer, and no parser round-trips arbitrary user prose
byte for byte. A whole-document re-render would churn spacing, list markers and tables the
user wrote. That churn shows up as noise in jj history and in Obsidian Sync conflicts.
Regex location is ruled out: block ids, callouts and fences nest in ways only a parser
gets right.

## Consequences

- A unit the parser cannot place is a hard error for mutations, and never a best-effort
  rewrite.
- Rendering a unit is the server's job, so its format lives in one place: the renderer and
  `template.md` are kept in step by a golden test.
