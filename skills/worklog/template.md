# Note template

The skeleton of a day's note. Fill in the `<angle brackets>`. `<icon>` is one of the status icons in SKILL.md. Keep empty buckets as `—`, so
tomorrow's open can tell "nothing" from "forgot".

Bucket names stay the same from day to day, because the next morning's open reads them back:

| Bucket | Holds |
|---|---|
| Decisions for you | HITL items: `ready-for-human`, `needs-triage`, `needs-info`, and open questions. Ordered by what each unblocks |
| Your actions | Manual operations only the user can run |
| Agent queue | `ready-for-agent` tickets, in dependency order |
| Sent to other repos | Cross-repo items and their herdr status |
| Outside input: chase | Waiting on a person outside the session |
| Waiting | Waiting on a first consumer, a producer, or a date |

````markdown
---
date: <YYYY-MM-DD>
previous: "[[<previous date>]]"
projects:
  - <owner/repo>
tags:
  - worklog
---

# Worklog — <YYYY-MM-DD>

**<HH:MM> UTC:** <one line: where the day stands>

## Objectives

| Objective | Done when | Progress |
|---|---|---|
| <objective> (<repo>) | <checkable condition> | <icon> <2/3 decisions · stack in review · missed: reason> |

## Next

1. <action> · <cost> · [[#^<item-id>|details]] · [▶ <verb> in <workspace>](obsidian://shell-commands/?vault=worklog&execute=<id>&_date=<date>&_item=<item-id>)
2. <manual action, no session can start it> · <cost> · [[#^<item-id>|details]]

## <icon> <owner/repo>

### <icon> Part 1 — <Human session | Agent batch | Land>: <title>, <running / done>

<one line per decision with its link · or a Ticket | PR | Outcome table · or the merge and its post-merge ops>

### <icon> What remains

#### <icon> Decisions for you

##### <icon> [#N](url): <question>

Unblocks <…>. <cost>. Route: `<skill>`, <why that route>.
[▶ <verb> in <workspace>](obsidian://shell-commands/?vault=worklog&execute=<id>&_date=<date>&_item=<item-id>) ^<item-id>

> [!prompt]- Prompt sent to <workspace label>
> ```herdr-prompt id=<item-id> to=<workspace label>
> <self-contained prompt, e.g. /grill-with-docs https://github.com/<o>/<r>/issues/N — the question and its context>
> ```

- <icon> [#M](url): <a decision with no prompt yet, one line>

#### <icon> Your actions

- —

#### <icon> Agent queue

- <icon> [#N](url) <title>, after [#M](url). A batch ready to start is a `#####` work item with its button and prompt

#### <icon> Sent to other repos

- <icon> <item> → <workspace label>, **<to send / sent / acked / done>**, <links>. While **to send**, it is a `#####` work item with its button and prompt

#### <icon> Outside input: chase

- —

#### <icon> Waiting

- —

## Carry-over

From [[<previous date>]].

| Item | State now | Verdict |
|---|---|---|
| <item with link> | <refreshed state> | <keep / defer / drop: reason> |

## Process

- —
````
