variable "name" {
  description = "Vault name. Globally unique and capped at 24 characters."
  type        = string

  validation {
    condition     = length(var.name) <= 24
    error_message = "Key Vault names are capped at 24 characters."
  }
}

variable "resource_group_name" {
  description = "Existing resource group that will hold the vault."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
}

variable "tenant_id" {
  description = "Entra tenant that authenticates vault callers."
  type        = string
}

variable "tags" {
  description = "Tags applied to the vault."
  type        = map(string)
  default     = {}
}
