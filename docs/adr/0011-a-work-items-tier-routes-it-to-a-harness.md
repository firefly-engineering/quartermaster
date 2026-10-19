# A work item's tier routes it to a harness, and the harness's kind picks the agent

Every prompt used to go to the one agent in a project's herdr workspace, and only `fresh`
dispatch could pick a model, through one `[harness.worker]`. Routing now has three steps:

1. A work item carries a **tier**, the class of its work, from a closed list the worklog
   skill owns. The queueing agent sets it from the item's route and raises it when the
   ticket warrants. The developer may override it.
2. `worklog.toml` names **harnesses** (`[harness.<name>]`: a herdr `kind` and `args`) and maps
   tiers to harness names in `[tiers]`. A tier it doesn't list goes to `default`, and so
   does an item with no tier.
3. The harness's kind picks the agent: the first available (`idle` or `done`) agent of that
   kind in the project's herdr workspace whose cwd is in the project's checkout. When none
   is available, the tool spawns one, creating the workspace first if the project has none.
   Each send records the chosen pane on the item.

The tier and the dispatch mode are independent. The tier picks the harness (who does the
work), the mode picks the checkout (where it happens).

This builds on [ADR 0003](0003-no-daemon-reconcile-on-read.md) and
[ADR 0005](0005-the-skill-is-the-single-source-of-workflow-rules.md). It amends
[ADR 0004](0004-dispatch-isolates-in-a-jj-workspace.md) only in naming a batch's checkout,
and ADR 0003 only in letting `refresh` write outside the note: it closes a finished `fresh`
dispatch's tab and removes its checkout, under guards. It still reconciles on read, with no
daemon.

## Why

The developer wants design, decisions, research and complex work on claude, and
well-specified implementation on a cheaper omp agent on GLM. Choosing per prompt is the
queueing agent's judgement, so the tier is a word from the workflow, not a model name. The
model behind each word is the developer's choice, so it lives in config.

Selection matches agents by kind, so config allows one harness per kind. An agent the user
started by hand counts when it matches, and a busy agent never receives a prompt: the tool
spawns a second one rather than queue behind a running turn.

The tier list, its meanings, the route-to-tier defaults and each tier's default mode live
in the skill ([ADR 0005](0005-the-skill-is-the-single-source-of-workflow-rules.md)). The
binary cuts only the tier list from the embedded skill, to validate config and
`item_upsert`. It never applies the route table or the mode column.

## Considered options

- **A `{harness, mode}` table in config.** Rejected: it puts workflow policy in the
  developer's config, and `[tiers]` values stop being plain harness names. The default mode
  per tier is a column in the skill's tier table, and the skill passes `mode` to
  `task_dispatch` itself
  ([#78](https://github.com/firefly-engineering/quartermaster/issues/78)).
- **A tier label on GitHub.** Rejected: a label is state to reconcile with the note
  ([ADR 0003](0003-no-daemon-reconcile-on-read.md)), and work items without a ref need a
  tier too. The tier is a field on the work item only
  ([#76](https://github.com/firefly-engineering/quartermaster/issues/76)).
- **The harness written into the note**, in the prompt block, the ▶ label or the callout.
  Rejected: config resolves it at send time, so a written harness goes stale when `[tiers]`
  changes. The recorded pane shows which agent ran the work
  ([#77](https://github.com/firefly-engineering/quartermaster/issues/77)).

## Consequences

- `[harness.worker]` is gone. Config refuses it, and a user `[harness.*]` or `[tiers]`
  replaces the built-in table whole.
- `refresh` reads a reply from the pane the send recorded, not from "the one agent in the
  workspace".
- The tool never closes a session agent or a workspace it created. `refresh` closes only
  `fresh` dispatch tabs, and removes their checkouts, once their item is done
  ([#98](https://github.com/firefly-engineering/quartermaster/issues/98),
  [#99](https://github.com/firefly-engineering/quartermaster/issues/99)).
- A worker that reports itself stuck has its item's tier raised to
  `complex-implementation`, and a human re-dispatches it
  ([#102](https://github.com/firefly-engineering/quartermaster/issues/102)).
- A `/mass-implement` batch is one work item with no ref, run on one harness. `task_dispatch`
  accepts its item id in place of a ref, and `fresh` names its checkout, branch and agent
  after that id ([#101](https://github.com/firefly-engineering/quartermaster/issues/101)).
