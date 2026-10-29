---
name: expand-contract
description: Use when changing a database schema that live code reads (rename, drop, retype, split a column or table), before writing the migration. Additive first, destructive last and alone, backfill in batches.
---

# Expand/contract schema migrations

A schema change is the riskiest migration because the data is the one thing you cannot roll back by reverting a deploy. The failure mode is coupling the schema change to the code change: rename a column in the same release that starts using the new name, and during the rollout window, when old and new code run at once, one of them queries a column that does not exist. Never change a column in place. Migrate in additive phases so old and new code are both valid at every step.

```
EXPAND ------------> MIGRATE ------------> CONTRACT
add the new column,  backfill existing     once no code reads the
nullable, alongside  rows, dual-write      old column, drop it in a
the old one          old+new from the app  later, separate deploy
```

Worked example, renaming `name` to `full_name`:

1. **Expand.** Add `full_name` as nullable. Deploy. Old code ignores it; nothing breaks.
2. **Dual-write.** The app writes both `name` and `full_name` on every insert and update. Deploy.
3. **Backfill.** Copy `name` to `full_name` for existing rows, in batches, so the table is not locked.
4. **Switch reads.** Point the app at `full_name`, keep writing both. Deploy and let it bake.
5. **Contract.** Stop writing `name`; then, in a separate, later deploy, drop the column.

Each step is independently deployable and reversible: if step 4 misbehaves, roll the code back and `full_name` is still being populated.

Rules:

- Additive first, destructive last and alone. Adds (a nullable column, a table, an index) are safe in any deploy; drops and renames get their own deploy after no code references the old shape.
- Every migration has a tested down path. Write and run the `down` before merging.
- Backfill in batches, off the hot path. One `UPDATE` over millions of rows locks the table; chunk and throttle it.
- Build large indexes without blocking writes (Postgres `CREATE INDEX CONCURRENTLY`).
- Decouple from code by feature flag when the cutover is risky.
- Before the PR: run `squawk` on the new migration files and the framework's drift check (`alembic check`, `drizzle-kit check`, `prisma migrate diff --exit-code`); the gate's `migrations` job runs the same two.

Source: this text is adapted from agent-skills (addyosmani/agent-skills 0.6.12, `skills/deprecation-and-migration/SKILL.md`, "Database Schema Migrations (Expand/Contract)"), MIT; the notice is in `LICENSES/agent-skills-MIT.txt` at the plugin root.
