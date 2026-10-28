output "id" {
  description = "Resource ID, what a VM's identity block takes."
  value       = azurerm_user_assigned_identity.this.id
}

output "name" {
  description = "Identity name."
  value       = azurerm_user_assigned_identity.this.name
}

output "client_id" {
  description = "Client ID, what a workload passes when it asks for a token as this identity."
  value       = azurerm_user_assigned_identity.this.client_id
}

output "principal_id" {
  description = "Object ID, the principal a role assignment is granted to."
  value       = azurerm_user_assigned_identity.this.principal_id
}
