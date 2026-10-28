# Deploy

Terraform deploys skybox's foundation: the shared Azure and Cloudflare resources every sandbox relies on. Sandboxes
themselves, with their tunnels and DNS records, are not Terraform resources. The skybox CLI creates them.

## Layout

```
deploy/
├── envs/prod/    # the one Terraform root; secrets.env holds 1Password references, prod.auto.tfvars the identifiers
├── modules/      # github-oidc, key-vault, network, uami
├── scripts/      # bootstrap-tfstate.py
└── tests/        # live tests against the applied foundation and the workflow logs
```

## What prod holds

In `rg-skybox-prod-gwc`, in germanywestcentral, every resource tagged `skybox:component=foundation`:

- `vnet-skybox-prod-gwc` with the private subnet `snet-sandboxes`. A VM there gets no default outbound public IP, and
  the subnet's security group `nsg-skybox-prod-gwc-sandboxes` has no rule beyond Azure's defaults, so nothing from the
  internet reaches it. A VM there has no outbound internet either until an outbound method is attached.
- A Key Vault holding `claude-code-oauth-token` and `cloudflare-api-token`, copied from 1Password on every apply.
- `id-skybox-sandbox-gwc`, the identity every sandbox VM carries. It reads `claude-code-oauth-token` and nothing else.
- `id-skybox-gha-prod-gwc`, the identity the `infra` workflow signs in as. It trusts one OIDC subject, the `prod`
  GitHub Environment, which admits `master` only. It holds Contributor on the resource group, Key Vault Secrets Officer
  on the vault, and may grant only the two Key Vault roles.
- In Cloudflare, the zone setting `websockets`, which SSH over Access needs. The zone is shared with other sites, so
  Terraform manages no other zone setting.

The state lives in `stskyboxamaretfstate`, in its own resource group `rg-skybox-tfstate`, apart from everything else.
Shared keys are off, so Terraform reads it and takes its lock lease with Entra.

## Keys

1Password is the only source of every key. The `skybox` vault holds them, and `envs/prod/secrets.env` names them as
`op://` references. On a Mac, `op run --env-file` resolves them. In GitHub Actions, `1password/load-secrets-action`
resolves them through the service account `skybox-github-actions`, whose token `OP_SERVICE_ACCOUNT_TOKEN` is the only
GitHub secret. Azure sign-in uses OIDC, so no Azure secret exists anywhere.

To rotate a key, change it in 1Password and run `infra` with `action: apply`. Key Vault then holds the new value.

## Running it

The `infra` workflow plans on every push to `master` that touches `deploy/`, and on a manual run. It applies only when
run by hand with `action: apply`, and then runs `tests/foundation.sh` against the result. Its `log-check` job fails
when the log shows any key's value.

From a Mac, with the Azure CLI signed in to the subscription:

```sh
terraform -chdir=deploy/envs/prod init
op run --env-file deploy/envs/prod/secrets.env -- terraform -chdir=deploy/envs/prod plan
op run --env-file deploy/envs/prod/secrets.env -- deploy/tests/foundation.sh
```

## First-time setup

1. `deploy/scripts/bootstrap-tfstate.py` creates the state account and grants you access to it.
2. Apply from a Mac as an Owner of the subscription. The first apply creates the deploy identity and its role
   assignments, which CI cannot grant itself.
3. `deploy/scripts/bootstrap-tfstate.py --deployer <github_actions_principal_id>` lets CI read and lock the state.
4. Set `AZURE_CLIENT_ID` in `scripts/setup-github.py` to the output `github_actions_client_id`, and run the script.
   It also creates the `prod` environment and pins it to `master`.
