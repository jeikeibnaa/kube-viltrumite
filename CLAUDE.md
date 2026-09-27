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
make verify      # go vet + unit tests + build — the pre-commit gate
make test        # unit tests
make generate    # regenerate deepcopy, CRD manifests and RBAC
make e2e         # e2e against kind cluster (target lands in S22)
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
| `/commit` | Staged smart commit with conventional format + emoji. Runs `make verify` first. |
| `/todo` | Manage `todos.md` — add, complete, list, remove tasks |
| `/update-docs` | Write the bilingual (English + Mongolian) session devlog, update the index and README |

---

## Session Workflow

One roadmap session (S20, S21, …) = one Claude Code session = one branch = one pull request.
The roadmap lives in `todos.md`; the scope, context and known issues for each session live in
`docs/HANDOFF.md`. Follow this order every session. Do not skip steps.

### Start of session
1. Read `docs/HANDOFF.md`, then run `/todo list` and take the first open session.
2. Sync main: `git checkout main && git pull`. If the previous session's PR is not merged yet,
   ask the user before branching.
3. Branch: `git checkout -b session/<N>-<short-topic>` (e.g. `session/21-embed-kb-docker`).

### During the session
- Stay inside that session's scope. Park anything else with `/todo add`.
- When writing or modifying AI prompts in `internal/ai/` — invoke the `senior-prompt-engineer` skill.

### End of session (mandatory, in this order)

**Step 1 — Code review**
Invoke the `code-reviewer` skill on every file touched this session.
Say: "Use the code-reviewer skill to review [files changed]"
Output all findings before moving on.

**Step 2 — Commit**
Run `/commit` — its gate is `make verify` (vet + test + build).
If the gate fails, fix before committing.

**Step 3 — Docs (bilingual)**
Run `/update-docs` — it writes the session devlog in English and Mongolian and updates the index.

**Step 4 — Todo sync**
Mark completed tasks: `/todo complete N`
Add follow-up tasks: `/todo add "..."`
Then commit the docs and todos (`📝 docs: S<N> devlog and todo sync`).

**Step 5 — Pull request**
Push the branch and open a PR against `main` with `gh pr create`.
Title: `S<N>: <goal>`. Body: summary, verification output, link to the devlog, and a short
Mongolian summary. If `gh` is unavailable, push and give the user the compare URL
`https://github.com/jeikeibnaa/kube-viltrumite/compare/main...<branch>?expand=1`.

**Step 6 — Queue the next session**
Create a spawn-task chip titled `Start S<N+1>: <topic>` with a self-contained prompt:
read CLAUDE.md and `docs/HANDOFF.md`, then run session S<N+1> from `todos.md` following this
Session Workflow. The user clicks it to start the next session in a fresh context. Where chips are
not available (terminal CLI), print that prompt in a code block for the user to paste into `claude`.

---

## Devlog Rules

- File: `docs/devlog/DEVLOG-YYYY-MM-DD-S<N>.md` (e.g. `DEVLOG-2026-09-27-S20.md`). The session
  number keeps two sessions on the same day from stacking into one file. Older date-only files
  keep their names.
- Bilingual: the English section comes first, then a full Mongolian (Cyrillic) section with the
  same headings. File names, CRD fields, commands and error text stay verbatim in both.
- Index: `docs/devlog/README.md` — one row per session, English and Mongolian descriptions
- Template is enforced by the `/update-docs` command
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
| Session devlogs | `docs/devlog/DEVLOG-YYYY-MM-DD-S<N>.md` |
| Roadmap + open tasks | `todos.md` |
| Session scope + context | `docs/HANDOFF.md` |
| Devlog index | `docs/devlog/README.md` |
| Architecture decisions | `docs/adr/` |
| API reference | `docs/api.md` |
| Project README | `README.md` |
