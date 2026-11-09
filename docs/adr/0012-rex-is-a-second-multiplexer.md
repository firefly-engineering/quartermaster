# Rex is the multiplexer: herdr support goes, and every send checks its prompt was taken

`worklog` drove its agents through herdr. Rex, a terminal multiplexer the developer now runs,
offers each of the ten calls send, dispatch and refresh make. It was added as a second
backend, picked by a `multiplexer` key and kept behind herdr's interface, as a trial
([#167](https://github.com/firefly-engineering/quartermaster/issues/167)). The trial passed,
and rex is now the only multiplexer
([#169](https://github.com/firefly-engineering/quartermaster/issues/169)):

- herdr support goes: `internal/herdr`, its `Mux` interface and the `multiplexer` key. The
  shared types live in `internal/rex`. A `worklog.toml` that still has the key fails to load,
  as any unknown key does.
- The docs keep quartermaster's own words, **workspace**, **tab** and **pane**, defined in
  GLOSSARY.md as rex's session, window and block. "herdr" stays only in ADRs and history.
- New prompt blocks are written as `qm-prompt`, like the `qm` dispatch marker. `herdr-prompt`
  blocks in old notes still parse, and old notes are not rewritten.
- Every send checks its prompt was taken. The agent must report `working` or `blocked`, or a
  status newer than the send, within 15 s. Otherwise the send fails with the last 5 lines of
  the pane's screen, and the item stays as it was.

## How rex stands in for herdr's calls

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
| `agent start` | label the block, paste the harness command into its shell, press Enter, wait for a status |
| `agent prompt` | a paste event, then Enter, then wait for the agent to take it |
| `agent read` | `rex capture --trim --unwrap`, last N lines |

Every rex call on a block or window needs its session, so the ids carry it. They contain no
whitespace, `--` or `>`, so they fit a dispatch marker as they are.

## Why

- **Rex carried every flow.** The trial's e2e ran on a copy of a real note, with `multiplexer
  = "rex"` ([results](https://github.com/firefly-engineering/quartermaster/issues/167#issuecomment-6739190130)).
  ▶ send opened a new rex session and spawned an agent. Session dispatch reused an idle
  agent. Fresh dispatch made its checkout and tab. `refresh` closed the tab, then removed the
  checkout. The developer's setup has run on rex since. Nothing herdr did is missing.
- **One backend, not two.** Keeping herdr means two clients, two sets of test fakes and a
  config choice for a backend nobody runs. With one backend, the interface is an abstraction
  nobody asked for, so the types live with their only client.
- **quartermaster's words, not either tool's.** The note, its `pane=` marker and the tool
  schemas already say workspace, tab and pane. Defining them as rex's terms changes no
  format. Using rex's own words would.
- **A prompt nobody took is a failure, not a send.** omp 18.18.2 reports `idle` while its
  setup wizard is up. The wizard took a fresh dispatch's prompt, and the dispatch reported
  success. Watching for the agent to start working catches that, and any other screen that
  eats a prompt, without reading the screen's words. The screen's last lines go in the error,
  so the cause shows at a glance.
- **An agent is the foreground program, its status what it reports.** Rex knows no agent
  kinds, but every block reports its foreground process. A harness that sends no OSC 7501
  still counts as an agent, with status `""`. The tab-close guards close a tab only for
  `idle` or `done`, so they keep its tab, and selection never prompts it.

## Considered options

- **Keep both backends, rex the default.** Rejected: it pays for herdr's client and fakes
  with no user, and "built-in defaults stay neutral" is simplest with one choice.
- **A neutral `internal/mux` package with an interface.** Rejected: rex is its only
  implementation, and the tests fake rex's CLI through a `Runner`, not the interface.
- **Keep `multiplexer`, accepting only `"rex"`.** Rejected: a key with one value is clutter
  forever. The developer's nix change bumps quartermaster and drops the line in one commit.
- **Rename `herdr-prompt` in every note.** Rejected: it rewrites history jj already keeps,
  and reading both names costs one branch in the parser.
- **Look for omp's wizard text on screen before typing.** Rejected: it is tied to one tool's
  wording. The delivery check catches it without that.
- **Report the idle-during-setup status to omp.** Not done: the delivery check makes quartermaster
  correct whatever a harness reports.
- **Raw `api call` for every step.** Rejected for creating sessions and windows: the raw
  layout schema needs a block flavor and options, while `rex new` and `rex window new` take a
  cwd and a label. Everything else uses `rex api call`, whose errors carry a code.

## Consequences

- A harness must report OSC 7501 to be started, picked or prompted. Claude Code and omp
  18.18.2 both do, and both run under a process named for their kind.
- `agent start` fails when its program reports nothing within 30s, and types nothing more.
  Claude reports nothing while it asks whether to trust a new folder, and a prompt's Enter
  would answer "No, exit".
- `agent start` types the command into the block's shell. Words are single-quoted, which
  covers POSIX shells and fish unless a word contains a backslash.
- A shell started through `login`, as macOS starts one, is one process deeper than the block's
  own process. The no-agent test allows for that.
- Rex reports a pane's cwd as the kernel has it, so a checkout configured through a symlink
  matches only once resolved.
- Rex is closed source. Its error code is read from the CLI's error line (`code=not_found`).
  This is the only text the client inspects.
- `agent list` costs one call per session plus two per block, about 16 ms each. That is fine
  at tens of panes.
