variable "name" {
  description = "Identity name."
  type        = string
}

variable "resource_group_name" {
  description = "Existing resource group that will hold the identity."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
}

variable "tags" {
  description = "Tags applied to the identity."
  type        = map(string)
  default     = {}
}
