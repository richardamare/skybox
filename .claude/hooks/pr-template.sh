#!/usr/bin/env bash
# A guidance comment left in a PR body becomes part of the squash commit message, so the stored body is checked after each write.

set -euo pipefail

input=$(cat)
command=$(jq -r '.tool_input.command // empty' <<<"$input")
[[ "$command" =~ gh[[:space:]]+pr[[:space:]]+(create|edit) ]] || exit 0

template="$CLAUDE_PROJECT_DIR/.github/PULL_REQUEST_TEMPLATE.md"
[[ -f "$template" ]] || exit 0

case "${1:?mode required}" in
  before)
    jq -n --rawfile template "$template" '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        additionalContext: ("Write the PR body from .github/PULL_REQUEST_TEMPLATE.md: keep every heading, fill every section by its guidance comment, then delete the comments. Tick every checkbox; a check that can run only after the merge goes in a PR comment you edit once it passes. Add Closes #<number> only for an issue that already exists.\n\n" + $template)
      }
    }'
    ;;

  after)
    url=$(jq -r '.tool_response | tostring' <<<"$input" | grep -oE 'https://github\.com/[^/]+/[^/]+/pull/[0-9]+' | tail -1 || true)
    [[ -n "$url" ]] || exit 0
    body=$(gh pr view "$url" --json body --jq .body | tr -d '\r')

    problems=()
    while IFS= read -r heading; do
      grep -qxF "$heading" <<<"$body" || problems+=("the heading \"$heading\" is missing")
    done < <(grep -E '^## ' "$template")
    grep -qF '<!--' <<<"$body" && problems+=("guidance comments are still in it")
    ((${#problems[@]})) || exit 0

    jq -n --arg url "$url" --arg problems "$(printf '%s; ' "${problems[@]}")" '{
      decision: "block",
      reason: ("The body of \($url) does not follow .github/PULL_REQUEST_TEMPLATE.md: \($problems[:-2]). Fix it with gh pr edit --body.")
    }'
    ;;
esac
