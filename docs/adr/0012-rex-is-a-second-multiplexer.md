# Rex is a second multiplexer, picked in config, behind herdr's interface

**Draft.** This records a trial ([#167](https://github.com/firefly-engineering/quartermaster/issues/167)).
Once the trial settles, either rex replaces herdr and this ADR is rewritten as that decision,
or the trial is dropped and this ADR is deleted with its code.

`worklog` reached agents only through herdr: ten calls in `internal/herdr`, used by send,
dispatch and refresh. The developer now runs Rex, a terminal multiplexer that offers each of
those calls. `worklog.toml` gains a top-level `multiplexer` key, `"herdr"` (the default) or
`"rex"`. `internal/rex` provides the same methods as `internal/herdr` and returns herdr's
types. Send, dispatch and refresh call either one through the `herdr.Mux` interface. No
workflow rule changes, and the note's format does not change.

## How rex stands in for herdr

| herdr | rex |
|---|---|
| workspace | session |
| tab | window |
| pane | block |
| pane id | `<session_id>/<block_id>` |
| tab id | `<session_id>/<window_id>` |
| `agent list` | every block in every session (`session.list_blocks`) running a program other than its shell |
| agent kind | the name of the block's foreground process |
| agent name | the block's label |
| agent status | the foreground program's own report (`program_status`, from OSC 7501) |
| `workspace create` | `rex new` |
| `tab create` | `rex window new --focus=false` |
| `tab close` | `session.close_window` |
| `agent start` | label the block, paste the harness command into its shell, press Enter, wait for the program |
| `agent prompt` | a paste event, then Enter |
| `agent read` | `rex capture --trim --unwrap`, last N lines |

Every rex call on a block or window needs its session, so the ids carry it. They contain no
whitespace, `--` or `>`, so they fit a dispatch marker as they are.

## Why

- **Behind herdr's interface, not a new one.** The trial must show whether rex can carry
  today's behaviour. Reusing herdr's types keeps send, dispatch and refresh unchanged and
  their tests valid. If rex wins, the package and its names move to neutral words then.
- **Picked in config, herdr the default.** Both can run on one machine and be compared on
  the same note. Nothing changes for a user who does not set the key.
- **An agent is the foreground program, its status what it reports.** Rex knows no agent
  kinds, but every block reports its foreground process. A harness that sends no OSC 7501
  still counts as an agent, with status `""`. The tab-close guards close a tab only for
  `idle` or `done`, so they keep its tab. Selection takes only `idle` or `done` agents, so it
  spawns a new one rather than prompt an agent of unknown state.

## Considered options

- **Rex only, herdr removed.** Rejected for now: it commits before the trial. It stays the
  likely end state.
- **A neutral multiplexer package first, then both backends.** Rejected for the trial: it
  renames every caller before anyone knows rex works. If rex is adopted, that rename comes
  with the decision.
- **Raw `api call` for every step.** Rejected for creating sessions and windows: the raw
  layout schema needs a block flavor and options, while `rex new` and `rex window new` take a
  cwd and a label. Everything else uses `rex api call`, whose errors carry a code.

## Consequences

- A harness must report OSC 7501 to be started or picked for a send. Claude Code and omp
  18.18.2 both do, and both run under a process named for their kind. `agent start` fails
  when its program reports nothing within 30s, and types nothing more. Claude reports nothing
  while it asks whether to trust a new folder, and a prompt's Enter would answer "No, exit".
- `agent start` treats the first status report as ready. omp reports `idle` while its startup
  screens are still up, a splash and, on a first run, a setup wizard. A prompt sent then is
  lost. Whether herdr's `agent start` waits past these screens is not checked.
- `agent start` types the command into the block's shell. Words are single-quoted, which
  covers POSIX shells and fish unless a word contains a backslash.
- A shell started through `login`, as macOS starts one, is one process deeper than the block's
  own process. The no-agent test allows for that.
- Rex is closed source. Its error code is read from the CLI's error line (`code=not_found`).
  This is the only text the client inspects.
- `agent list` costs one call per session plus two per block, about 16 ms each. That is fine
  at tens of panes.
