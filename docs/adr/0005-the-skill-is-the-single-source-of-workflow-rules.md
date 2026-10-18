# The worklog skill is the single source of workflow rules

The draft wanted the workflow served only through MCP prompts, with no markdown
distributed. The existing skill is markdown that Claude Code loads by trigger.
quartermaster keeps one source, `skills/worklog/`, and ships it twice:

- as a skill bundle (the `skills` flake package), installed into each harness's skill
  directory;
- embedded in the binary and served as the `workflow_rules` MCP prompt.

The skill keeps the judgement: phases, routes and triage. Every write goes through the
tools.

## Why

Skills trigger on their own, and prompts must be asked for. Harnesses without skills, like
a bare MCP client, still get the rules. Embedding at build time means the two copies cannot
drift.

## Consequences

- Rules the server enforces are removed from the skill text. The skill points at the
  tools, so it never restates a constraint the code already checks.
