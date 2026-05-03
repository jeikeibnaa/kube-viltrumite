---
allowed-tools: Read, Write, Edit, Bash
argument-hint: [--devlog | --index | --readme | --all]
description: Write today's session devlog, update the devlog index, and sync README status for kube-viltrumite
---

# Update Docs — Kube-Viltrumite

Systematically update project documentation after a session: $ARGUMENTS

## Current State

- Today's date: !`date +%Y-%m-%d`
- Devlog directory: !`ls docs/devlog/ 2>/dev/null | tail -5 || echo "docs/devlog/ not found"`
- Existing devlog today: !`cat docs/devlog/DEVLOG-$(date +%Y-%m-%d).md 2>/dev/null || echo "NO DEVLOG YET TODAY"`
- Devlog index: !`tail -10 docs/devlog/README.md 2>/dev/null || echo "No index yet"`
- Files changed this session: !`git diff --name-only HEAD 2>/dev/null | head -20`
- Recent commits: !`git log --oneline -5 2>/dev/null`
- Current todos: !`cat todos.md 2>/dev/null | head -30 || echo "No todos.md"`

## Task

### 1. Write today's devlog

Create (or update if it already exists) `docs/devlog/DEVLOG-$(date +%Y-%m-%d).md`.

Use this exact template — fill every section, do not leave placeholders:

```markdown
# DEVLOG — [YYYY-MM-DD] — Session [N]: [Goal Title]

## Goal
[One sentence: what this session set out to accomplish]

## Prompt Used
[The exact prompt or intent that started this session]

## Files Touched
[List every file read or modified, with a one-line note on what changed]

## What Was Built
[Concrete description of what was implemented or changed]

## Errors Hit
[Any errors encountered and how they were resolved. "None" if clean session.]

## Test Results
[Output of make test / make e2e, or "Tests not run" with reason]

## Key Decisions
[Architecture or design decisions made, with brief rationale]

## Code Review Findings
[Paste findings from code-reviewer skill, or "Not run" — but it must be run]

## Prompt Engineering Notes
[Notes from senior-prompt-engineer skill if prompts were touched. "N/A" if not.]

## Next Session Preview
[What to do next — be specific enough to resume without re-reading code]
```

### 2. Update the devlog index

Update `docs/devlog/README.md`. If it doesn't exist, create it with this header:

```markdown
# Devlog Index — Kube-Viltrumite

| Date | Session | Goal | Key Files | Status |
|------|---------|------|-----------|--------|
```

Add one new row for today's session:
`| YYYY-MM-DD | N | [goal] | [key files, comma-separated] | ✅ Done |`

Never remove existing rows.

### 3. Update README.md (only if --readme or --all flag)

If `--readme` or `--all` is passed:
- Update the "Current Status" section if it exists
- Add any new commands or CRD fields to usage examples
- Do NOT rewrite the README — only update stale sections

## Guidelines

- DO NOT create new spec files
- DO NOT modify files in `api/v1alpha1/` — those are generated
- DO update `docs/devlog/` and `docs/devlog/README.md` every session
- One devlog file per day — if the file already exists, append a new session block with `---` separator
- Be specific: vague devlogs are useless for resuming next session
- If `make test` output is available in context, paste it verbatim in Test Results
- If code-reviewer findings are in context, paste them verbatim in Code Review Findings

## Output

After completing, print a summary:
1. Devlog file written: `docs/devlog/DEVLOG-YYYY-MM-DD.md`
2. Index updated: yes/no
3. README updated: yes/no
4. Any issues encountered
