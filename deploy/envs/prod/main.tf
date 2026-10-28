resource "random_string" "suffix" {
  length  = 6
  upper   = false
  special = false
}

locals {
  workload       = "skybox"
  env            = "prod"
  location_short = "gwc"
  suffix         = random_string.suffix.result

  # The control plane reads state from skybox: tags, and leaves anything tagged as the foundation alone.
  tags = {
    "skybox:component" = "foundation"
  }
}

resource "azurerm_resource_group" "main" {
  name     = "rg-${local.workload}-${local.env}-${local.location_short}"
  location = var.location
  tags     = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

module "network" {
  source = "../../modules/network"

  name                = "${local.workload}-${local.env}-${local.location_short}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  tags                = local.tags
}

module "key_vault" {
  source = "../../modules/key-vault"

  name                = "kv-${local.workload}-${local.env}-${local.suffix}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  tenant_id           = var.tenant_id
  tags                = local.tags
}

module "github_oidc" {
  source = "../../modules/github-oidc"

  name                = "id-${local.workload}-gha-${local.env}-${local.location_short}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  environment         = local.env
  subject_prefix      = var.github_oidc_subject_prefix
  tags                = local.tags
}

# Every sandbox VM carries this identity, which reads the Claude token and no other secret.
module "sandbox_identity" {
  source = "../../modules/uami"

  name                = "id-${local.workload}-sandbox-${local.location_short}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  tags                = local.tags
}

resource "azurerm_key_vault_secret" "claude_code_oauth_token" {
  name         = "claude-code-oauth-token"
  value        = var.claude_code_oauth_token
  key_vault_id = module.key_vault.id
  tags         = local.tags

  depends_on = [
    azurerm_role_assignment.github_actions_kv_secrets_officer,
    azurerm_role_assignment.operator_kv_secrets_officer,
  ]
}

resource "azurerm_key_vault_secret" "cloudflare_api_token" {
  name         = "cloudflare-api-token"
  value        = var.cloudflare_api_token
  key_vault_id = module.key_vault.id
  tags         = local.tags

  depends_on = [
    azurerm_role_assignment.github_actions_kv_secrets_officer,
    azurerm_role_assignment.operator_kv_secrets_officer,
  ]
}

resource "azurerm_role_assignment" "sandbox_claude_token_reader" {
  scope                = azurerm_key_vault_secret.claude_code_oauth_token.resource_versionless_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = module.sandbox_identity.principal_id
  principal_type       = "ServicePrincipal"
}

# Contributor on the one resource group, so the deploy identity reaches nothing else in the subscription.
resource "azurerm_role_assignment" "github_actions_contributor" {
  scope                = azurerm_resource_group.main.id
  role_definition_name = "Contributor"
  principal_id         = module.github_oidc.principal_id
  principal_type       = "ServicePrincipal"
}

resource "azurerm_role_assignment" "github_actions_kv_secrets_officer" {
  scope                = module.key_vault.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = module.github_oidc.principal_id
  principal_type       = "ServicePrincipal"
}

# By object ID rather than the caller's identity, so CI and a Mac do not revoke each other on every apply.
resource "azurerm_role_assignment" "operator_kv_secrets_officer" {
  scope                = module.key_vault.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = var.operator_object_id
  principal_type       = "User"
}

# Contributor cannot write role assignments, so the deploy identity may grant and revoke only the two Key Vault roles
# above, inside this resource group. Every other role stays out of its reach.
locals {
  github_actions_assignable_role_ids = [
    "4633458b-17de-408a-b874-0445c86b69e6", # Key Vault Secrets User
    "b86a8fe4-44ce-4948-aee5-eccb2c155cd7", # Key Vault Secrets Officer
  ]
}

resource "azurerm_role_assignment" "github_actions_rbac_admin" {
  scope                = azurerm_resource_group.main.id
  role_definition_name = "Role Based Access Control Administrator"
  principal_id         = module.github_oidc.principal_id
  principal_type       = "ServicePrincipal"

  condition_version = "2.0"
  condition         = <<-EOT
    (
     (
      !(ActionMatches{'Microsoft.Authorization/roleAssignments/write'})
     )
     OR
     (
      @Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {${join(", ", local.github_actions_assignable_role_ids)}}
     )
    )
    AND
    (
     (
      !(ActionMatches{'Microsoft.Authorization/roleAssignments/delete'})
     )
     OR
     (
      @Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {${join(", ", local.github_actions_assignable_role_ids)}}
     )
    )
  EOT
}

# SSH over Cloudflare Access and dev server live reload both run over WebSockets. The zone is shared with other
# sites, so the foundation manages only the settings skybox depends on.
resource "cloudflare_zone_setting" "websockets" {
  zone_id    = var.cloudflare_zone_id
  setting_id = "websockets"
  value      = "on"
}

# Every sandbox's SSH hostname, <name>-sbx.<zone>, sits behind this one application, so only the owner's Access
# identity reaches a sandbox's tunnel. The hostnames stay one level below the zone, where Universal SSL covers them.
resource "cloudflare_zero_trust_access_policy" "sandbox_owner" {
  account_id       = var.cloudflare_account_id
  name             = "skybox sandbox owner"
  decision         = "allow"
  session_duration = "24h"
  include = [{
    email = { email = var.access_email }
  }]
}

resource "cloudflare_zero_trust_access_application" "sandboxes" {
  account_id                = var.cloudflare_account_id
  name                      = "skybox sandboxes"
  type                      = "self_hosted"
  domain                    = "*-sbx.${var.cloudflare_zone}"
  destinations              = [{ type = "public", uri = "*-sbx.${var.cloudflare_zone}" }]
  session_duration          = "24h"
  app_launcher_visible      = false
  auto_redirect_to_identity = false
  policies = [{
    id         = cloudflare_zero_trust_access_policy.sandbox_owner.id
    precedence = 1
  }]
}
