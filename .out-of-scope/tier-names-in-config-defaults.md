# Tier names in the built-in config defaults

The built-in `[tiers]` table in `internal/config` names tiers as string literals:
`basic-implementation` and `refactoring` go to the `glm` harness, the rest to `default`. It
stays that way. The tier list is not cut into the defaults from the skill, and the skill's
tier table does not gain a default-harness column.

## Why this is out of scope

The worklog skill owns the tier list ([ADR 0005](../docs/adr/0005-the-skill-is-the-single-source-of-workflow-rules.md),
[ADR 0011](../docs/adr/0011-a-work-items-tier-routes-it-to-a-harness.md)), and the binary
reads it with `skills.Tiers()`. The defaults look like a second copy of that list, but they
are not: they are config policy, a choice of which tiers go to the cheaper model. A policy
has to name the tiers it routes, and the developer's own `worklog.toml` names them the
same way.

Moving that choice into the skill would put config in the skill, against ADR 0005's split
between workflow rules (the skill) and the developer's machine and models (config).

Drift is caught before it ships. `config.Load` validates the merged `[tiers]` against
`skills.Tiers()`, and `TestLoad`'s "defaults alone when no user file exists" case loads the
built-in table. A skill that renames or drops a tier the defaults name fails that test in
CI, so no user ever sees a config that refuses to load.

## Prior requests

- [#151](https://github.com/firefly-engineering/quartermaster/issues/151): "Built-in [tiers] defaults copy tier names the skill owns"
