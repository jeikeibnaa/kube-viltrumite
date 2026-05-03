# Prompt to paste into Claude Code
# Copy everything between the --- markers and paste it as your first message

---
Read CLAUDE.md fully before doing anything else.

You have the following tools installed in this project. Learn them now so you use them correctly throughout our session:

**Skills** (you invoke these by intent — I will tell you when):
- Code reviewer: `.claude/skills/code-reviewer/SKILL.md`
- Senior prompt engineer: `.claude/skills/senior-prompt-engineer/SKILL.md`

**Commands** (I type these, you execute them):
- `/commit` — smart conventional commit with lint+build check
- `/todo` — manage todos.md task list
- `/update-docs` — write devlog + update index after session

**Session contract you must follow:**
1. Start by running `/todo list` to show open tasks
2. During the session: when I touch any file in `internal/ai/`, invoke the senior-prompt-engineer skill
3. End of session — in this exact order:
   a. Run code-reviewer skill on all files we touched
   b. Run `/commit`
   c. Run `/update-docs`
   d. Run `/todo` to sync completed/new tasks

Do not skip end-of-session steps. If I try to end without doing them, remind me.

Confirm you've read CLAUDE.md and understood the tools. Then run `/todo list`.
---
