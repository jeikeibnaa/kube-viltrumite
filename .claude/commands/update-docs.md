---
allowed-tools: Read, Write, Edit, Bash
argument-hint: [--devlog | --index | --readme | --all]
description: Write the session devlog (English + Mongolian), update the devlog index, and sync README status for kube-viltrumite
---

# Update Docs — Kube-Viltrumite

Systematically update project documentation after a session: $ARGUMENTS

## Current State

- Today's date: !`date +%Y-%m-%d`
- Current branch (carries the session number): !`git branch --show-current`
- Devlog directory: !`ls docs/devlog/ 2>/dev/null | tail -5 || echo "docs/devlog/ not found"`
- Devlogs already written today: !`ls docs/devlog/DEVLOG-$(date +%Y-%m-%d)* 2>/dev/null || echo "NONE TODAY"`
- Devlog index: !`tail -10 docs/devlog/README.md 2>/dev/null || echo "No index yet"`
- Files changed this session (vs main): !`git diff --name-only main 2>/dev/null | head -30`
- Recent commits: !`git log --oneline -5 2>/dev/null`
- Current todos: !`cat todos.md 2>/dev/null | head -30 || echo "No todos.md"`

## Task

### 1. Write the session devlog

Create `docs/devlog/DEVLOG-YYYY-MM-DD-S<N>.md`, where `<N>` is the roadmap session number
(from the branch name `session/<N>-...` or `todos.md`). One session = one file — never append a
second session to an existing devlog.

The devlog is bilingual. Write the English section first, then a full Mongolian (Cyrillic) section
with the same content. Rules for the Mongolian section:

- Translate prose fully and naturally; do not leave English sentences behind.
- Keep technical identifiers verbatim: file paths, CRD fields, Go identifiers, commands, flags,
  error messages, and widely used terms (Kubernetes, Helm, CRD, reconciler, PR).
- Verbatim blocks (test output, command output) appear only in the English section. The Mongolian
  section summarizes them in one or two sentences and points to the English block.

Use this exact template — fill every section, do not leave placeholders:

```markdown
# DEVLOG — [YYYY-MM-DD] — Session [N]: [Goal Title]

**Languages:** [English](#english) · [Монгол](#монгол)

---

## English

### Goal
[One sentence: what this session set out to accomplish]

### Prompt Used
[The exact prompt or intent that started this session]

### Files Touched
[List every file read or modified, with a one-line note on what changed]

### What Was Built
[Concrete description of what was implemented or changed]

### Errors Hit
[Any errors encountered and how they were resolved. "None" if clean session.]

### Test Results
[Output of make verify / make e2e, or "Tests not run" with reason]

### Key Decisions
[Architecture or design decisions made, with brief rationale]

### Code Review Findings
[Paste findings from code-reviewer skill, or "Not run" — but it must be run]

### Prompt Engineering Notes
[Notes from senior-prompt-engineer skill if prompts were touched. "N/A" if not.]

### Next Session Preview
[What to do next — be specific enough to resume without re-reading code]

---

## Монгол

### Зорилго
### Ашигласан prompt
### Өөрчилсөн файлууд
### Юу хийсэн бэ
### Гарсан алдаа ба шийдэл
### Тестийн үр дүн
### Гол шийдвэрүүд
### Code review-ийн дүгнэлт
### Prompt engineering тэмдэглэл
### Дараагийн session
[Same content as the English section, in Mongolian, under each heading above]
```

### 2. Update the devlog index

Update `docs/devlog/README.md`. Add one new row per session; never remove existing rows:

`| [DEVLOG-YYYY-MM-DD-S<N>.md](./DEVLOG-YYYY-MM-DD-S<N>.md) | Session N | [English description] | [Mongolian description] |`

### 3. Update README.md (only if --readme or --all flag)

If `--readme` or `--all` is passed:
- Update the "What works today" table if a session changed the state of an area
- Add any new commands or CRD fields to usage examples
- Do NOT rewrite the README — only update stale sections

## Guidelines

- DO NOT create new spec files
- DO NOT modify files in `api/v1alpha1/` — those are generated
- DO update `docs/devlog/` and `docs/devlog/README.md` every session
- One devlog file per session — never stack two sessions in one file
- Be specific: vague devlogs are useless for resuming next session
- If `make verify` output is available in context, paste it verbatim in Test Results
- If code-reviewer findings are in context, paste them verbatim in Code Review Findings

## Output

After completing, print a summary:
1. Devlog file written: `docs/devlog/DEVLOG-YYYY-MM-DD-S<N>.md` (English + Mongolian)
2. Index updated: yes/no
3. README updated: yes/no
4. Any issues encountered
