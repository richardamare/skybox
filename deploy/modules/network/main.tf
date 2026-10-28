resource "azurerm_virtual_network" "this" {
  name                = "vnet-${var.name}"
  resource_group_name = var.resource_group_name
  location            = var.location
  address_space       = [var.address_space]
  tags                = var.tags
}

# A private subnet: Azure gives a VM here no default outbound public IP, so a VM reaches the internet only through an
# outbound method someone attaches on purpose.
resource "azurerm_subnet" "sandboxes" {
  name                            = "snet-sandboxes"
  resource_group_name             = var.resource_group_name
  virtual_network_name            = azurerm_virtual_network.this.name
  address_prefixes                = [cidrsubnet(var.address_space, 8, 0)]
  default_outbound_access_enabled = false
}

# Sandboxes are reached through an outbound Cloudflare tunnel, so nothing needs an inbound rule. The empty list makes a
# rule added outside Terraform show up as drift in the next plan.
resource "azurerm_network_security_group" "sandboxes" {
  name                = "nsg-${var.name}-sandboxes"
  resource_group_name = var.resource_group_name
  location            = var.location
  security_rule       = []
  tags                = var.tags
}

resource "azurerm_subnet_network_security_group_association" "sandboxes" {
  subnet_id                 = azurerm_subnet.sandboxes.id
  network_security_group_id = azurerm_network_security_group.sandboxes.id
}
