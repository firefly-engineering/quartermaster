# Refs are `owner/repo#N`

One note spans several projects, so an integer issue id is ambiguous. Every ref the server
handles is `owner/repo#N`. A config entry maps each `owner/repo` to its checkout path and
the label of its herdr workspace.

## Consequences

- A bare `#N` is accepted only where the project is already in scope, such as inside a
  project section.
- In the note, refs render as full links, as the skill already requires.
