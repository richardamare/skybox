output "client_id" {
  description = "Client ID that azure/login and the azurerm provider sign in as."
  value       = azurerm_user_assigned_identity.this.client_id
}

output "principal_id" {
  description = "Object ID, the principal role assignments are granted to."
  value       = azurerm_user_assigned_identity.this.principal_id
}

output "subject" {
  description = "The one OIDC subject that may sign in as this identity."
  value       = azurerm_federated_identity_credential.environment.subject
}
