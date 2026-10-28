#!/usr/bin/env bash
# Puts the "Changing code" rule from CLAUDE.md beside every code file change, including changes made by a shell command.

set -euo pipefail

input=$(cat)
session=$(jq -r '.session_id // "default"' <<<"$input")
marker="${TMPDIR:-/tmp}/skybox-bash-start-$session"

is_code() {
  case "$1" in
    *.md | *.json | *.lock | *-lock.yaml) return 1 ;;
  esac
}

remind() {
  local event=$1 lead=$2 rule
  rule=$(awk '/^## Changing code/ { on = 1; next } on && /^## / { exit } on' "$CLAUDE_PROJECT_DIR/CLAUDE.md")
  [[ -n "$rule" ]] || return 0
  jq -n --arg event "$event" --arg text "$lead"$'\n'"$rule" \
    '{ hookSpecificOutput: { hookEventName: $event, additionalContext: $text } }'
}

case "${1:?mode required}" in
  edit)
    file=$(jq -r '.tool_input.file_path // empty' <<<"$input")
    [[ -n "$file" ]] && is_code "$file" || exit 0
    remind PreToolUse "Before this edit, apply the rule from CLAUDE.md:"
    ;;

  bash-start)
    touch "$marker"
    ;;

  bash-end)
    [[ -f "$marker" ]] || exit 0
    changed=()
    while IFS= read -r file; do
      is_code "$file" && changed+=("${file#"$CLAUDE_PROJECT_DIR"/}")
    done < <(find "$CLAUDE_PROJECT_DIR" \
      \( -name node_modules -o -name .git -o -name dist \) -prune -o \
      -type f -newer "$marker" -print)
    rm -f "$marker"
    ((${#changed[@]})) || exit 0
    remind PostToolUse "That command changed these code files: ${changed[*]}. Check each against the rule from CLAUDE.md:"
    ;;
esac
