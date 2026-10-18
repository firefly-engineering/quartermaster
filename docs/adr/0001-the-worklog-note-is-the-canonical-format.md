# The worklog note is the canonical format, and manual actions are task checkboxes

The first draft gave the server its own note format: a `## Today's Engineering Plan` list of
issues with checkbox markers (`- [/]`, `- [?]`, `- [-]`) as states. The `worklog` skill
already has a format in daily use, built to lower the barrier to action: objectives with
their done-conditions, an ordered *Next* with ▶ buttons, one section per project with Parts
and *What remains* buckets, carry-over and process. quartermaster adopts the worklog format.
The draft's states map onto the worklog's status icons, plus 🔍 for *in review*.

One part of the draft stays: a **manual action** is a plain task checkbox (`- [ ]`, or
`1. [ ]` in *Next*). That lets the user tick it in Obsidian without knowing the icon
convention.

## Why

Two formats would mean two parsers and two templates. Every skill phase would also need
two descriptions. The worklog format carries information the draft has no place for, such
as objectives, routes, prompts and cross-repo items.

Non-standard checkbox markers (`[/]`, `[?]`) need an Obsidian theme or plugin to render.
Icons render everywhere. Plain `[ ]`/`[x]` is the one marker Obsidian toggles with a click,
which is why manual actions keep it.

## Consequences

- Checkboxes belong to the user. The server reads a ticked box as `DONE`, and never unticks
  one.
- Agent-tracked work items carry icons and no checkbox, so the two kinds never compete for
  one line.
