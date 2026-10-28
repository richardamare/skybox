output "id" {
  description = "Vault resource ID, the scope a Key Vault role assignment is granted on."
  value       = azurerm_key_vault.this.id
}

output "name" {
  description = "Vault name, as `az keyvault` wants it."
  value       = azurerm_key_vault.this.name
}

output "vault_uri" {
  description = "Vault base URI, trailing slash included."
  value       = azurerm_key_vault.this.vault_uri
}
