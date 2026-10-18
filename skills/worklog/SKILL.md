---
name: worklog
description: >-
  Keep the day's worklog note in the Obsidian vault (~/src/github.com/sigma/worklog):
  one living note per working day, across every project, led by progress on the day's
  objectives and the next actions as ▶ buttons. Use to open the day (objectives, triage
  of yesterday's leftovers), to record a human unblock session or its decisions, to queue
  or record an AFK agent batch (mass-implement results, PR stacks, merges), to capture a
  new bug, HITL need or work item sent to another repo (IaC agent over herdr), to refine
  the day's objectives, and to close the day. Also use when the user asks "where are we",
  "what's next" or "what's left" for today.
---

# Worklog

One **note** per working day holds the day's live state across every project. It is a
**living document**: each update rewrites it into the current state, and the vault's jj
history keeps the timeline. Its job is to **lower the barrier to action**. Someone glancing
at it sees how far the objectives have got and what to do next, and starts the next
action with one click.

## The vault

- Vault repo: `~/src/github.com/sigma/worklog` (jj, no remote), unless the user names
  another. Call it `$VAULT`. The Obsidian vault is `$VAULT/worklog`, named `worklog`.
- Notes: `$VAULT/worklog/YYYY-MM-DD.md` (Obsidian's daily-notes default). The date is the
  local date (`date +%F`). Times are UTC from the clock (`date -u +%H:%M`). When the user
  gives a time for an event, use it; never estimate one.
- The **previous note** is the latest dated note before today: weekends and days off
  leave gaps.
- Obsidian may have created today's note empty. Empty counts as missing.

## The note

The layout comes from [`template.md`](template.md). Its order is what makes the note
actionable:

1. **Status**: one line, where the day stands.
2. **Objectives**: each with its progress (`2/3 decisions`, `stack in review`) and state.
3. **Next**: the user's next actions, at most five, in the order to take them. Each
   action is one line with a cost, and a ▶ button whenever a session can start it
   (see [Buttons](#buttons)).
4. One `##` section per project worked on today, in the order they first appear. Each
   holds the day's Parts, then *What remains*.
5. **Carry-over** and **Process** come last.

**Status icons** let the user skip what is done. Each objective's *Progress*, each
project and Part heading, and each work item and bucket starts with one:

| Icon | Means |
|---|---|
| ✅ | done |
| 🔄 | in progress: a session or batch is on it, or some of it is done |
| ⏳ | blocked or waiting: on IaC, a person, a date or another item |
| ⬜ | not started |
| ❌ | missed or dropped, with the reason |

A heading's icon sums up what is under it: ✅ only when everything under it is done. Refresh
every icon at each update.

**Next** is rebuilt at every update, from the *What remains* buckets and the routes.
Each action is a step the user takes now: a decision to settle, a batch to start, a stack
to review, an IaC request to send, a manual operation that is due. Each one links to its
work item, where its context and prompt live.

Write tight. One line per item. A decision recorded on a ticket gets a line and a link,
not a paragraph. A finished Part shrinks to a line or a short table; the jj history keeps
the long version. Only what lives nowhere else is kept in full, such as a command run by
hand or a resource created by hand.

Refs are always links: `[#871](https://github.com/<o>/<r>/issues/871)` in a project's
section, and `[IaC#564](…)` across repos. Link every PR, run and commit, including in
the status line and in prose.

## The day's cycle

The day runs **open**, then repeats **unblock → AFK → land** as often as needed, then
**close**. **Capture** happens any time. Work out the phase from what the user says.
When they only ask "where are we", refresh the note and report from it.

Every phase ends the same way: refresh state, rewrite the note, commit
(see [Saving](#saving)).

### Open

Create the note from [`template.md`](template.md).

1. Collect **every open item anywhere in the previous note**. That includes *What
   remains*, unmet objectives, deferred carry-over rows, *Process* to-dos, and review
   findings left as judgement calls. Each becomes a carry-over row.
2. Refresh each row against its source of truth (`gh issue view`, `gh pr view`, herdr
   replies). An item closed overnight is reported, not triaged.
3. Ask for the day's **objectives**. Each names its project and a checkable
   done-condition, such as "epic #639 closed" or "stack merged". Offer candidates from
   the carry-over and the open epics. **Milestones first when one earns it**: read the
   projects' open GitHub milestones (`gh api repos/<o>/<r>/milestones`), and any that is
   **late** (past its due date) or **urgent** (due within seven days) offers "make
   progress on <milestone>" as the first candidate — the day's primary objective unless
   the user says otherwise. Its done-condition names the milestone work that moves it:
   the coverage register (#265) and the milestone's open issues for trunk.
4. Triage the carry-over with the user, one verdict per row:
   - **keep**: in scope today, and it moves into its project's buckets.
   - **defer**: stays on the carry-over, so tomorrow's open finds it again.
   - **drop**: closed, wontfix, or not relevant, with the reason.

   A row with no ticket gets a proposal: file a ticket, or carry it explicitly.

Done when every objective has a done-condition, every carry-over row has a verdict, and
*Next* holds the first actions.

### Unblock

A human session that turns open questions into agent-ready work.

1. Build the **decision list**: every HITL item in scope (`ready-for-human`,
   `needs-triage`, `needs-info`, open questions in tickets). Order it by what each
   decision unblocks. Give each a one-line question, a rough cost, and its
   [route](#routes).
2. Work through the list with the user, one decision at a time. A quick one is settled
   inline; a larger one goes to its route's skill, through a button. Record each decision
   where it belongs: a ticket comment, an ADR or spec PR, or a relabel to
   `ready-for-agent`. The user's live answer approves recording that decision on that
   ticket. Propose new tickets, epics and other repos' items before creating them.
3. Write a *Part N — Human session*: one line and a link per decision. Rebuild the
   **Agent queue**: the `ready-for-agent` tickets, their dependency order, and the batch's
   global context.

Done when every decision taken is recorded outside the note, and *Next* leads with the
batch's ▶ button.

### AFK

A batch runs through `/implement` for one ticket, or `/mass-implement` for several.
Both are user-invoked, so the batch starts from the ▶ button in *Next*, which sends the
command to the project's session. For example:
`/mass-implement arbora-partners/trunk #883 #871 — global context: <epic URL>`.

Record a *Part N — Agent batch* marked **running**. When it reports back, rewrite the
Part as a table with one row per ticket: PR, then outcome (`built`, **part only**, or back
to HITL with the reason). Then:

- Move each ticket that went back to HITL into *Decisions for you*.
- Move each new ticket into its bucket.
- List each review finding left as a judgement call under the Part.

### Land

The user reviews and merges. Record:

- the merge (stack and merge commit);
- the tickets closed, by commit or by hand;
- the post-merge operations: releases, deploys, live checks, temporary grants and their
  removal.

Then mark each objective whose done-condition now holds as ✅.

### Capture

Any time:

- A **new bug** gets a proposed ticket. Once filed, it goes to its bucket.
- A **new HITL need** goes to *Decisions for you*, with the question it blocks on.
- A **refined objective** is rewritten in place, with `(refined HH:MM UTC: <reason>)`.
- **Another repo's work** follows [Other repos](#other-repos).
- A **process change** goes to *Process*.

### Close

Make tomorrow's open trivial.

1. Refresh every open item from GitHub, PRs and herdr replies.
2. Compact each finished Part.
3. Rewrite the status line as the day's outcome: objectives met and missed, and the next
   step with what gates it.
4. Make sure every *What remains* item has a bucket, a link and its next action.
5. Give every found issue a ticket or an explicit carry. Nothing lives only in prose.
6. Make *Next* tomorrow's first actions.

Done when someone could open tomorrow from *Objectives*, *Next* and *What remains*
alone.

## Routes

Every open item carries a **route**: the next skill that moves it. Its ▶ button sends
that skill's command.

| Shape | Route |
|---|---|
| Fog: a new idea or requirement too big for one session | `/wayfinder`. Its decision tickets join the decision list, and the objective links the map |
| One plan or design to stress-test | `/grill-with-docs` when the outcome should land as an ADR or glossary term, otherwise `/grill-me` |
| A raw or under-specified ticket | `/triage` |
| A settled discussion with no tickets yet | `/to-spec`, then `/to-tickets` |
| A question only someone outside the session can answer | `/to-questionnaire`, then *Outside input: chase* |
| A quick question the user can answer now | inline, in the unblock session |
| One `ready-for-agent` ticket | `/implement` |
| Several `ready-for-agent` tickets | `/mass-implement` |
| Work another repo owns | [Other repos](#other-repos) |

A route changes as the item moves. A grilled ticket becomes `ready-for-agent`. An
implemented one goes back to `/grill-me` when the batch finds a gap.

## Buttons

A ▶ button sends a prompt to a herdr session: the user's agent for a project, in the
herdr workspace labelled with the project's name (`trunk`, `IaC`). Clicking it types
that prompt into the session for the user; nothing is pasted by hand.

Any web page can open the button's `obsidian://` URI. So the URI carries only the note's
date and an item id. The prompt text lives in the note, inside the **work item** it
starts, so the user reads the context, the button and exactly what it sends together.
The work item is the item's entry in its project's *What remains*, never in a Part. A work
item that carries a prompt is a `#####` heading under its bucket, never a list item:
Obsidian renders a callout nested in a list as raw text. Its paragraph ends with the button
and then the block id `^<item-id>`, and the prompt follows as a top-level callout:

````markdown
##### ⬜ [#819](…): is a window Item admissible for a research Pipeline's Run?

Every research window Run is refused until it is settled. ~20 min. Route:
`/grill-with-docs`, because it touches ADRs 0013 and 0018.
[▶ grill in trunk](obsidian://shell-commands/?vault=worklog&execute=<id>&_date=2026-10-16&_item=grill-819) ^grill-819

> [!prompt]- Prompt sent to trunk
> ```herdr-prompt id=grill-819 to=trunk
> /grill-with-docs https://github.com/arbora-partners/trunk/issues/819 — <the question and its context>
> ```
````

The callout starts collapsed (`[!prompt]-`), so the prompt stays out of the way until the
user opens it. `worklog-send` reads through the `>` markers. Items without a prompt stay
one-line list entries, and each bucket is a `####` heading.

*Next* stays one line per action, and repeats the button with a link to the work item:

```markdown
3. Settle #819 · 20 min · [[#^grill-819|details]] · [▶ grill in trunk](obsidian://shell-commands/?…&_item=grill-819)
```

- The button's label names what it does and where: `▶ grill in trunk`, `▶ send to IaC`,
  `▶ run batch in trunk`.

- `<id>` is the Shell commands plugin's id for the command that runs `worklog-send`.
  Read it from `$VAULT/worklog/.obsidian/plugins/obsidian-shellcommands/data.json`: it is
  the `id` of the `shell_commands` entry whose command contains `worklog-send`.
- When that file or entry is missing, leave out the buttons but keep the prompt blocks in
  their work items, and point the user at [`buttons-setup.md`](buttons-setup.md) once.
- Item ids are lowercase letters, digits and `-`, unique within the note. A prompt is
  self-contained, because the session receiving it may not have the note's context.
- `worklog-send` adds a closing line to every prompt: when the work is done, update the
  worklog's `^<item-id>` work item and refresh *Next*. Leave that line out of the block.
  A session that receives it runs the matching phase here, against that work item.
- Once an action is done, its work item leaves its bucket and becomes a plain line in its
  Part, with no `^<item-id>`. Drop its button and prompt, and its line in *Next*.

## Other repos

Some work belongs to another repo; the current repo's `CLAUDE.md` draws the boundary.
For trunk, `arbora-partners/IaC` owns what Arbora operates. That work goes to the repo's
own session over herdr. It is tracked in the **originating** project's *Sent to other
repos* bucket, because that project is waiting on it.

1. Write the work item under *Sent to other repos*, with its button and a `herdr-prompt`
   block with `to=<workspace label>`. The prompt says
   what is needed and why, links the ticket it unblocks, says how the result is verified,
   and asks for a reply with links (issue, PR, run).
2. Put its ▶ button in *Next*, and record the item as **to send**.
3. When the user asks you to send it, send it yourself with the same script:
   `~/.claude/skills/worklog/scripts/worklog-send $VAULT/worklog <date> <id>`. It fails
   if `HERDR_ENV` is unset, or if the workspace is missing or has no single agent; leave
   the item **to send** and say why.
4. Track status (**to send**, **sent**, **acked**, **done**) and the links as they arrive.
   Check for replies at each refresh (`herdr agent read`; see the `herdr` skill).

When the user works directly in another repo that day, that repo gets its own project
section.

## Saving

- **Re-read before you write.** Sessions for other projects edit the same note. Re-read
  it just before editing, and change only the sections this update touches.
- **Commit each update** as its own jj change, touching only the note:

  ```bash
  jj -R "$VAULT" commit -m "worklog(<date>): <phase>: <headline>" 'root:"worklog/<date>.md"'
  ```

  The `root:` fileset anchors the path to the vault: a bare path resolves from the
  shell's cwd, not from `-R`.

  If `$VAULT` is not a jj repo, write the note anyway and tell the user nothing was
  committed.

## Reporting

End each phase with the note's status line, its *Next* list, and the note's path.
