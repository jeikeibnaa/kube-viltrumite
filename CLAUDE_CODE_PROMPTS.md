# Mid-Session Prompts for Kube-Viltrumite
# Paste these at the right moment in Claude Code

---

## When starting a new session (paste this first, always)

Read CLAUDE.md fully before doing anything else.

You have skills and commands installed — read them now:
- `.claude/skills/code-reviewer/SKILL.md`
- `.claude/skills/senior-prompt-engineer/SKILL.md`
- Commands: /commit, /todo, /update-docs

Session contract: start with `/todo list`, end with code-review → /commit → /update-docs → /todo sync. Do not skip.

Confirm you've read CLAUDE.md. Then run `/todo list`.

---

## When you want a code review mid-session

Use the code-reviewer skill to review these files:
- [file1]
- [file2]

Focus on: Go idioms, error handling, interface compliance, Kubernetes controller patterns.
Output all findings before continuing.

---

## When touching internal/ai/ prompts

I'm about to modify a prompt in internal/ai/. Before I show you the change,
read the senior-prompt-engineer skill at `.claude/skills/senior-prompt-engineer/SKILL.md`.
Then review what I'm about to write and suggest improvements.

---

## When ending a session

We're wrapping up. Follow the end-of-session steps from CLAUDE.md in order:

1. Use the code-reviewer skill on all files we touched this session. List them first, then review each.
2. Run /commit
3. Run /update-docs
4. Run /todo list and tell me which items to mark complete, then apply them

Do not stop until all 4 steps are done.

---

## When adding a new task

/todo add "[task description]"

---

## When you want to check what's left

/todo list

---

## When debugging a reconciler loop

Read internal/controller/ and apply the code-reviewer skill.
Specifically check: idempotency, context propagation, status condition updates, requeue strategy.

---

## When adding a new AI adapter (new provider)

I'm adding a new AIProvider adapter. Before writing any code:
1. Read internal/ai/provider.go to understand the interface
2. Read the senior-prompt-engineer skill for prompt design guidance
3. Confirm the interface methods I need to implement

Then proceed with implementation.

---

## When generating CRDs

After any changes to api/v1alpha1/, run:
  make generate

Then use the code-reviewer skill on the changed api/ files before committing.

---
