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

variable "environment" {
  description = "GitHub Environment the identity is federated to."
  type        = string
}

variable "subject_prefix" {
  description = "sub_claim_prefix from `gh api repos/<owner>/<repo>/actions/oidc/customization/sub`. A repository on immutable subjects carries owner and repository ids in it, so `repo:<owner>/<name>` never matches."
  type        = string
}

variable "tags" {
  description = "Tags applied to the identity."
  type        = map(string)
  default     = {}
}
