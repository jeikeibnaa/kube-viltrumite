#!/usr/bin/env bash
# Company guardrails. Claude Code runs this before every Bash, Read, Edit and Write call.
#
# Master copy: jeikeibnaa/hq staff/hooks/guard.sh. Product repos carry a synced copy at
# .claude/hooks/guard.sh. Change the master, then run hq/scripts/sync-staff.sh; never edit a copy.
#
# Exit 0 allows the call. Exit 2 blocks it and shows the reason to Claude.
# GUARD_ALLOW_MAIN_PUSH=1 (set in hq's own settings only) allows committing and pushing to main.
set -u

input=$(cat)

# The tool name is a plain word, so a regex is enough and needs no JSON parser.
tool=$(printf '%s' "$input" | sed -n 's/.*"tool_name"[[:space:]]*:[[:space:]]*"\([A-Za-z]*\)".*/\1/p' | head -n 1)

# get <key>: a field of tool_input, else of the hook event itself.
get() {
  local key=$1 py
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$input" | jq -r --arg k "$key" '(.tool_input[$k] // .[$k]) // empty' 2>/dev/null
    return
  fi
  if command -v node >/dev/null 2>&1; then
    printf '%s' "$input" | node -e '
      let s = ""; process.stdin.on("data", c => s += c).on("end", () => {
        const d = JSON.parse(s), k = process.argv[1];
        const v = (d.tool_input && d.tool_input[k] != null) ? d.tool_input[k] : d[k];
        if (v != null) process.stdout.write(String(v));
      });' "$key" 2>/dev/null
    return
  fi
  # On Windows, python3 can be the Microsoft Store stub, so check that it actually runs.
  for py in python3 python; do
    if command -v "$py" >/dev/null 2>&1 && "$py" -c 'import json' >/dev/null 2>&1; then
      printf '%s' "$input" | "$py" -c '
import json, sys
d = json.load(sys.stdin); k = sys.argv[1]
v = (d.get("tool_input") or {}).get(k, d.get(k))
sys.stdout.write("" if v is None else str(v))' "$key" 2>/dev/null
      return
    fi
  done
  # No parser at all: match against the raw event, which errs on the side of blocking.
  printf '%s' "$input"
}

block() {
  printf 'Blocked by company guardrail: %s\n' "$1" >&2
  printf 'Policy: jeikeibnaa/hq policy/authority.md. Do not work around this; if it has to happen, say so in your report to the CEO.\n' >&2
  exit 2
}

allow_main() { [ "${GUARD_ALLOW_MAIN_PUSH:-0}" = 1 ]; }

# True when the session's repo has main or master checked out.
on_main() {
  local dir br
  dir=$(get cwd)
  [ -n "$dir" ] && [ -d "$dir" ] || dir=.
  br=$(git -C "$dir" rev-parse --abbrev-ref HEAD 2>/dev/null) || return 1
  [ "$br" = main ] || [ "$br" = master ]
}

check_bash() {
  local c re
  # Pad with spaces so every token has a separator on both sides.
  c=" $(get command | tr '\r\n\t' '   ') "

  re='git[[:space:]][^;&|]*--no-verify'
  [[ $c =~ $re ]] && block "--no-verify skips the repo's git hooks."

  re='git[[:space:]]+(filter-branch|filter-repo|replace)[[:space:]]'
  [[ $c =~ $re ]] && block "rewriting git history is a CEO decision."

  re='git[[:space:]]+push[[:space:]]'
  if [[ $c =~ $re ]]; then
    re='git[[:space:]]+push[^;&|]*(--force|--mirror|[[:space:]]-[a-zA-Z]*f[a-zA-Z]*[[:space:]]|[[:space:]]\+[^[:space:]])'
    [[ $c =~ $re ]] && block "force pushes rewrite shared history. Only the CEO does that."
    re='git[[:space:]]+push[^;&|]*(--tags|--delete|[[:space:]]-d[[:space:]]|[[:space:]]:[^[:space:]]|[[:space:]]v[0-9]+\.[0-9])'
    [[ $c =~ $re ]] && block "pushing tags or deleting remote branches is a release decision for the CEO."
    if ! allow_main; then
      re='git[[:space:]]+push[^;&|]*[[:space:]:](refs/heads/)?(main|master)[[:space:]]'
      [[ $c =~ $re ]] && block "nothing is pushed to main. Push a branch and open a PR."
      on_main && block "this repo is on main. Create a branch first (CLAUDE.md has the naming rule)."
    fi
  fi

  if ! allow_main; then
    re='git[[:space:]]+(commit|merge|cherry-pick|revert)[[:space:]]'
    [[ $c =~ $re ]] && on_main && block "this repo is on main. Create a branch before committing."
  fi

  re='gh[[:space:]]+pr[[:space:]]+merge[[:space:]]'
  [[ $c =~ $re ]] && block "the CEO merges PRs. On kube-viltrumite, low-risk PRs are merged by the auto-merge workflow, not by agents."
  re='gh[[:space:]]+release[[:space:]]+(create|delete|edit|upload)[[:space:]]'
  [[ $c =~ $re ]] && block "releases are the CEO's call."
  re='gh[[:space:]]+repo[[:space:]]+(delete|edit|archive|unarchive|rename)[[:space:]]'
  [[ $c =~ $re ]] && block "repository settings belong to the CEO."
  re='gh[[:space:]]+(secret|variable|ssh-key|gpg-key)[[:space:]]'
  [[ $c =~ $re ]] && block "secrets and keys belong to the CEO."
  re='gh[[:space:]]+auth[[:space:]]+(token|logout|refresh|switch)[[:space:]]'
  [[ $c =~ $re ]] && block "agents don't read or change GitHub credentials."

  re='gh[[:space:]]+api[^;&|]*(-X|--method)[[:space:]]*DELETE'
  [[ $c =~ $re ]] && block "DELETE calls on the GitHub API are not allowed for agents."
  re='gh[[:space:]]+api[^;&|]*(protection|rulesets|collaborators|/hooks|/keys|/secrets|/variables|/environments|/actions/permissions)'
  if [[ $c =~ $re ]]; then
    re='((-X|--method)[[:space:]]*(PUT|PATCH|POST)|[[:space:]](-f|-F|--field|--raw-field|--input)[[:space:]])'
    [[ $c =~ $re ]] && block "branch protection, access and secrets settings belong to the CEO."
  fi

  # Reading credential files. Example files carry placeholders, so drop them first.
  local s
  s=$(printf '%s' "$c" | sed -E 's/\.env\.(example|sample|template|dist)//g')
  re='(^|[[:space:];&|(])(cat|less|more|head|tail|bat|type|grep|egrep|rg|awk|sed|strings|xxd|od|base64|source)[[:space:]][^;&|]*(\.env(\.[A-Za-z0-9_-]+)?|\.credentials\.json|id_(rsa|ed25519|ecdsa)|\.kube/config|\.aws/credentials|\.(pem|p12|pfx))([[:space:]"'"'"')]|$)'
  [[ $s =~ $re ]] && block "that file holds credentials. Secrets stay out of agent context."

  # Changing the guardrails themselves.
  re='\.claude/(settings[A-Za-z.]*\.json|hooks/)'
  if [[ $c =~ $re ]]; then
    re='(^|[[:space:];&|(])(rm|mv|cp|tee|chmod|truncate|ln)[[:space:]]|sed[[:space:]]+-i|>'
    [[ $c =~ $re ]] && block "guardrail files change only through hq/scripts/sync-staff.sh in a PR the CEO reviews."
  fi
}

check_file() {
  local mode=$1 p
  p=$(get file_path | tr '\\' '/')
  [ -n "$p" ] || p=$(get notebook_path | tr '\\' '/')
  [ -n "$p" ] || return 0

  case "$p" in
    *.env.example|*.env.sample|*.env.template|*.env.dist) ;;
    .env|*/.env|.env.*|*/.env.*|*.env) block "that is an .env file. Secrets stay out of agent context; use .env.example." ;;
  esac
  case "$p" in
    *.credentials.json|*/id_rsa|*/id_ed25519|*/id_ecdsa|*.pem|*.p12|*.pfx|*/.kube/config|*/.aws/credentials|*/.ssh/*)
      block "that file holds credentials." ;;
  esac
  if [ "$mode" != Read ]; then
    case "$p" in
      */.claude/settings.json|*/.claude/settings.local.json|*/.claude/hooks/*)
        block "guardrail files change only through hq/scripts/sync-staff.sh in a PR the CEO reviews." ;;
    esac
  fi
}

case "$tool" in
  Bash) check_bash ;;
  Read|Edit|Write|MultiEdit|NotebookEdit) check_file "$tool" ;;
esac
exit 0
