#!/usr/bin/env bash
# Fails when the log of one job of a workflow run holds the value of any key from secrets.env, or the service
# account token. The values come from the environment and are never printed.
#
#   deploy/tests/log-has-no-secret.sh <owner/repo> <run id> <run attempt> <job name>

set -euo pipefail

repo=$1
run=$2
attempt=$3
job_name=$4
secrets_file=${SECRETS_FILE:-deploy/envs/prod/secrets.env}

job=$(gh api "repos/$repo/actions/runs/$run/attempts/$attempt/jobs" --jq '.jobs[] | select(.name == "'"$job_name"'") | .id')
log=$(mktemp)
trap 'rm -f "$log"' EXIT
gh api "repos/$repo/actions/jobs/$job/logs" >"$log"
if [[ ! -s $log ]]; then
  echo "FAIL the $job_name job's log is empty, so there is nothing to check"
  exit 1
fi

names=(OP_SERVICE_ACCOUNT_TOKEN)
while IFS='=' read -r name _; do
  [[ $name =~ ^[A-Z_]+$ ]] && names+=("$name")
done <"$secrets_file"

leaked=()
for name in "${names[@]}"; do
  value=${!name:-}
  if [[ -z $value ]]; then
    echo "FAIL $name is not set, so the log cannot be checked for it"
    exit 1
  fi
  grep -qF -- "$value" "$log" && leaked+=("$name")
done

if ((${#leaked[@]})); then
  echo "FAIL the $job_name job's log shows the value of: ${leaked[*]}"
  exit 1
fi
echo "ok   the $job_name job's log ($(wc -l <"$log") lines) shows none of: ${names[*]}"
