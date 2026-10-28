terraform {
  # Backend blocks cannot interpolate. Keep these literals in step with deploy/scripts/bootstrap-tfstate.py.
  backend "azurerm" {
    subscription_id      = "d349301a-5b5b-4fa0-b6a9-9a30d80b7047"
    resource_group_name  = "rg-skybox-tfstate"
    storage_account_name = "stskyboxamaretfstate"
    container_name       = "tfstate"
    key                  = "skybox-prod.tfstate"

    # Shared-key access is off on the account, so state is read, written and locked with Entra.
    use_azuread_auth = true
  }
}
