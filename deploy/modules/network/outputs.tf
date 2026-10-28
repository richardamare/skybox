output "name" {
  description = "Virtual network name."
  value       = azurerm_virtual_network.this.name
}

output "sandbox_subnet_id" {
  description = "Subnet a sandbox's NIC joins."
  value       = azurerm_subnet.sandboxes.id
}

output "sandbox_subnet_name" {
  description = "Name of the sandbox subnet."
  value       = azurerm_subnet.sandboxes.name
}

output "sandbox_nsg_name" {
  description = "Network security group on the sandbox subnet."
  value       = azurerm_network_security_group.sandboxes.name
}
