variable "name" {
  description = "Base name, such as skybox-prod-gwc. The network is vnet-<name> and its security group nsg-<name>-sandboxes."
  type        = string
}

variable "resource_group_name" {
  description = "Existing resource group that will hold the network."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
}

variable "address_space" {
  description = "A /16 for the network. The sandbox subnet takes its first /24."
  type        = string
  default     = "10.60.0.0/16"
}

variable "tags" {
  description = "Tags applied to the network and its security group."
  type        = map(string)
  default     = {}
}
