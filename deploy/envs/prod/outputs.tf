output "resource_group_name" {
  value = azurerm_resource_group.main.name
}

output "key_vault_name" {
  value = module.key_vault.name
}

output "virtual_network_name" {
  value = module.network.name
}

output "sandbox_subnet_id" {
  value = module.network.sandbox_subnet_id
}

output "sandbox_subnet_name" {
  value = module.network.sandbox_subnet_name
}

output "sandbox_nsg_name" {
  value = module.network.sandbox_nsg_name
}

output "sandbox_identity_id" {
  description = "User-assigned identity every sandbox VM carries."
  value       = module.sandbox_identity.id
}

output "sandbox_identity_client_id" {
  value = module.sandbox_identity.client_id
}

output "github_actions_client_id" {
  description = "AZURE_CLIENT_ID in scripts/setup-github.py."
  value       = module.github_oidc.client_id
}

output "github_actions_principal_id" {
  description = "Pass it to bootstrap-tfstate.py --deployer."
  value       = module.github_oidc.principal_id
}

output "github_actions_subject" {
  value = module.github_oidc.subject
}

output "control_plane_job_name" {
  value = azurerm_container_app_job.control_plane.name
}
