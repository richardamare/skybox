mock_provider "azurerm" {}
mock_provider "cloudflare" {}
mock_provider "random" {}

variables {
  cloudflare_zone_id      = "zone"
  cloudflare_api_token    = "cloudflare-token"
  claude_code_oauth_token = "claude-token"
}

run "only_the_prod_environment_can_sign_in" {
  command = plan

  assert {
    condition     = output.github_actions_subject == "${var.github_oidc_subject_prefix}:environment:prod"
    error_message = "The deploy identity trusts a subject other than the prod environment."
  }
}

run "sandboxes_read_only_the_claude_token" {
  command = plan

  assert {
    condition     = azurerm_role_assignment.sandbox_claude_token_reader.role_definition_name == "Key Vault Secrets User"
    error_message = "The sandbox identity holds a role other than Key Vault Secrets User."
  }

  assert {
    condition     = azurerm_key_vault_secret.claude_code_oauth_token.value == var.claude_code_oauth_token
    error_message = "Key Vault does not get the Claude token from 1Password."
  }

  assert {
    condition     = azurerm_key_vault_secret.cloudflare_api_token.value == var.cloudflare_api_token
    error_message = "Key Vault does not get the Cloudflare token from 1Password."
  }
}

run "every_taggable_resource_carries_the_foundation_tag" {
  command = plan

  assert {
    condition = alltrue([
      for tags in [
        azurerm_resource_group.main.tags,
        azurerm_key_vault_secret.claude_code_oauth_token.tags,
        azurerm_key_vault_secret.cloudflare_api_token.tags,
      ] : tags["skybox:component"] == "foundation"
    ])
    error_message = "A resource lacks the skybox:component=foundation tag."
  }
}
