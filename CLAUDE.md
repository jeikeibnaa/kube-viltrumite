# Kube-Viltrumite

A Kubernetes operator for AI-powered upgrade planning.
Named after the Viltrumites from Invincible — superior beings who impose order.

---

## Project Structure

```
cmd/operator/          — operator entrypoint
cmd/cli/               — vilt kubectl plugin
internal/ai/           — AIProvider interface + adapters
internal/controller/   — StackUpgrade reconciler
internal/scanner/      — cluster + git scanners
internal/planner/      — compatibility matrix + upgrade ordering
api/v1alpha1/          — CRD type definitions
knowledge/             — YAML compatibility database
ui/                    — React dashboard
docs/devlog/           — session devlogs (one file per session)
```

---

## Absolute Rules

- The operator NEVER imports a specific AI provider directly.
  Always code to the `AIProvider` interface in `internal/ai/provider.go`.
- Never stack devlog entries into a single file. One session = one file.
- Never create new spec files. Update existing docs only.

---

## Test Commands

```bash
make test        # unit tests
make e2e         # e2e against kind cluster
make generate    # regenerate CRD manifests
```

---

## Available Tools

These tools are installed and ready. You MUST use them at the right moments
(see Workflow section below). Do not skip them.

### Skills (loaded by intent — describe what you want)

| Skill | Location | When to use |
|---|---|---|
| `code-reviewer` | `.claude/skills/code-reviewer/SKILL.md` | After writing/editing any Go, YAML, or UI code |
| `senior-prompt-engineer` | `.claude/skills/senior-prompt-engineer/SKILL.md` | When writing or improving AI prompts inside `internal/ai/` |

### Commands (type these explicitly)

| Command | What it does |
|---|---|
| `/commit` | Staged smart commit with conventional format + emoji. Runs lint+build first. |
| `/todo` | Manage `todos.md` — add, complete, list, remove tasks |
| `/update-docs` | Sync devlog, README, and docs after a session |

---

## Session Workflow

Follow this order every session. Do not skip steps.

### During the session
1. Check open tasks: `/todo list`
2. Work on the goal.
3. When writing or modifying AI prompts in `internal/ai/` — invoke the `senior-prompt-engineer` skill.

### End of session (mandatory, in this order)

**Step 1 — Code review**
Invoke the `code-reviewer` skill on every file touched this session.
Say: "Use the code-reviewer skill to review [files changed]"
Output all findings before moving on.

**Step 2 — Commit**
Run `/commit` — it will lint, build, and format the commit message.
If lint/build fails, fix before committing.

**Step 3 — Docs**
Run `/update-docs` — it writes today's devlog and updates the index.

**Step 4 — Todo sync**
Mark completed tasks: `/todo complete N`
Add next session tasks: `/todo add "..."`

---

## Devlog Rules

- File: `docs/devlog/DEVLOG-YYYY-MM-DD.md`
- Index: `docs/devlog/README.md` — one row per session
- Template is enforced by the `/update-docs` command (see below)
- Never edit devlog files manually mid-session — the command handles it

---

## Go Conventions

- Interface-first: code to interfaces, not concrete types
- Error wrapping: `fmt.Errorf("context: %w", err)`
- Contexts: always propagate `ctx` through call chains
- Logging: use structured logging (`log.Info("msg", "key", val)`)
- Tests: table-driven tests, subtests with `t.Run`
- Generated files: always run `make generate` after CRD changes

---

## AI Provider Conventions

When working in `internal/ai/`:
- Prompts live in `internal/ai/prompts/` as `.go` files with string constants
- Every prompt change → invoke `senior-prompt-engineer` skill for review
- Never hardcode model names outside adapter files

---

## Kubernetes / Operator Conventions

- CRD changes always need `make generate` before testing
- Reconciler loops must be idempotent
- Use `ctrl.Result{RequeueAfter: ...}` not `ctrl.Result{Requeue: true}` for timed requeues
- Status conditions follow `metav1.Condition` pattern

---

## Documentation Locations

| What | Where |
|---|---|
| Session devlogs | `docs/devlog/DEVLOG-YYYY-MM-DD.md` |
| Devlog index | `docs/devlog/README.md` |
| Architecture decisions | `docs/adr/` |
| API reference | `docs/api.md` |
| Project README | `README.md` |
