# No daemon: every process is stateless and `refresh` reconciles on demand

The draft proposed a heartbeat against herdr, which needs a long-lived process. An MCP stdio
server lives as long as its client, and each harness starts its own. quartermaster has no
daemon in v1. All state lives in the note, GitHub and herdr. The `refresh` tool reads all
three and applies only the transitions the evidence proves.

## Why

A daemon brings a socket, a lifecycle, an install unit per OS, and a second source of truth
to keep in step with the note. The failure the heartbeat catches, a dead worker leaving an
item 🔄, only matters when someone reads the note. Reconciling at read time catches it at
that moment.

## Consequences

- A crashed worker is noticed at the next `refresh`, not when it dies.
- If push-style notification is ever needed, it can be added as a separate process that
  calls the same engine. The tools do not change.
