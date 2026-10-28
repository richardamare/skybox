#!/usr/bin/env bash
# Tests the applied foundation against live Azure and 1Password. Run it after an apply, with the Azure CLI signed in,
# the keys from secrets.env in the environment, and OP_SERVICE_ACCOUNT_TOKEN set:
#
#   op run --env-file deploy/envs/prod/secrets.env -- deploy/tests/foundation.sh
#
# On a Mac without OP_SERVICE_ACCOUNT_TOKEN, the service account test is skipped.

set -euo pipefail

TF_DIR=${TF_DIR:-deploy/envs/prod}
failures=0

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }
tf() { terraform -chdir="$TF_DIR" "$@"; }
output() { tf output -raw "$1"; }

rg=$(output resource_group_name)
vault=$(output key_vault_name)

test_second_plan_has_no_changes() {
  local code=0
  tf plan -input=false -detailed-exitcode -lock-timeout=60s >/dev/null || code=$?
  if ((code == 0)); then pass "a plan after the apply has no changes"; else fail "a plan after the apply exits $code, not 0"; fi
}

# A plan in the background takes the lock as any apply would. Once the state blob is leased, a second apply must fail.
# Without -auto-approve and with input off, that apply could never change anything even if it got the lock.
test_concurrent_apply_fails_on_state_lock() {
  local backend account container key holder state="" out
  backend=$TF_DIR/.terraform/terraform.tfstate
  account=$(jq -r .backend.config.storage_account_name "$backend")
  container=$(jq -r .backend.config.container_name "$backend")
  key=$(jq -r .backend.config.key "$backend")

  tf plan -input=false -lock-timeout=0s >/dev/null 2>&1 &
  holder=$!
  for _ in $(seq 120); do
    state=$(az storage blob show --auth-mode login --account-name "$account" --container-name "$container" \
      --name "$key" --query properties.lease.state -o tsv)
    [[ $state == leased ]] && break
    sleep 0.5
  done
  if [[ $state != leased ]]; then
    wait "$holder" || true
    fail "the first plan never held the state lock, so the test proved nothing"
    return
  fi

  if out=$(tf apply -input=false -lock-timeout=0s 2>&1); then
    fail "a second apply ran while the first held the state lock"
  elif grep -q "Error acquiring the state lock" <<<"$out"; then
    pass "a second apply at the same time fails on the state lock"
  else
    fail "a second apply failed, but not on the state lock"
  fi
  wait "$holder" || fail "the plan holding the lock failed"
}

test_key_vault_holds_1password_values() {
  local name variable value
  for pair in claude-code-oauth-token:TF_VAR_claude_code_oauth_token cloudflare-api-token:TF_VAR_cloudflare_api_token; do
    name=${pair%%:*}
    variable=${pair#*:}
    value=$(az keyvault secret show --vault-name "$vault" --name "$name" --query value -o tsv)
    if [[ -n ${!variable:-} && $value == "${!variable}" ]]; then
      pass "Key Vault secret $name matches 1Password"
    else
      fail "Key Vault secret $name differs from 1Password"
    fi
  done
}

test_subnet_gives_no_default_public_ip() {
  local private
  private=$(az network vnet subnet show -g "$rg" --vnet-name "$(output virtual_network_name)" \
    -n "$(output sandbox_subnet_name)" --query defaultOutboundAccess -o tsv)
  if [[ $private == false ]]; then
    pass "the sandbox subnet is private, so a VM there gets no default outbound public IP"
  else
    fail "the sandbox subnet has defaultOutboundAccess=$private"
  fi
}

test_no_inbound_rule_open_to_internet() {
  local open
  open=$(az network nsg show -g "$rg" -n "$(output sandbox_nsg_name)" -o json | jq -r '
    [.securityRules[], .defaultSecurityRules[]
      | select(.direction == "Inbound" and .access == "Allow")
      | select([.sourceAddressPrefix // empty, (.sourceAddressPrefixes // [])[]]
          | any(. == "*" or . == "Internet" or . == "0.0.0.0/0"))
      | .name] | join(", ")')
  if [[ -z $open ]]; then
    pass "the sandbox subnet's security group has no inbound rule open to the internet"
  else
    fail "inbound rules open to the internet: $open"
  fi
}

test_service_account_reads_only_skybox_vault() {
  if [[ -z ${OP_SERVICE_ACCOUNT_TOKEN:-} ]]; then
    echo "skip the service account test: OP_SERVICE_ACCOUNT_TOKEN is not set"
    return
  fi
  local vaults
  vaults=$(op vault list --format json | jq -r '[.[].name] | sort | join(",")')
  if [[ $vaults == skybox ]]; then
    pass "the service account reads the skybox vault and no other"
  else
    fail "the service account reads these vaults: $vaults"
  fi
}

test_second_plan_has_no_changes
test_concurrent_apply_fails_on_state_lock
test_key_vault_holds_1password_values
test_subnet_gives_no_default_public_ip
test_no_inbound_rule_open_to_internet
test_service_account_reads_only_skybox_vault

((failures == 0)) || { echo "$failures test(s) failed"; exit 1; }
