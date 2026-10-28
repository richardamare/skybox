resource "azurerm_user_assigned_identity" "this" {
  name                = var.name
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = var.tags
}

# The only subject is the GitHub Environment, whose deployment branch policy decides which refs reach it. A run
# outside that environment carries a pull_request or ref subject, which no credential here matches.
resource "azurerm_federated_identity_credential" "environment" {
  name                      = "gha-env-${var.environment}"
  user_assigned_identity_id = azurerm_user_assigned_identity.this.id
  audience                  = ["api://AzureADTokenExchange"]
  issuer                    = "https://token.actions.githubusercontent.com"
  subject                   = "${var.subject_prefix}:environment:${var.environment}"
}
