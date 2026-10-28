mock_provider "azurerm" {}

variables {
  name                = "skybox-test"
  resource_group_name = "rg-test"
  location            = "germanywestcentral"
}

run "network_module_gives_no_public_ip_and_opens_no_port" {
  command = plan

  assert {
    condition     = azurerm_subnet.sandboxes.default_outbound_access_enabled == false
    error_message = "The sandbox subnet gives VMs a default outbound public IP."
  }

  assert {
    condition     = length(azurerm_network_security_group.sandboxes.security_rule) == 0
    error_message = "The sandbox security group has a custom rule. Sandboxes are reached through Cloudflare only."
  }
}
