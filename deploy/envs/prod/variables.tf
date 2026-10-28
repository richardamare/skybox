variable "subscription_id" {
  description = "Azure subscription that holds the foundation."
  type        = string
}

variable "tenant_id" {
  description = "Entra tenant that owns the subscription."
  type        = string
}

variable "location" {
  description = "Azure region of the foundation and every sandbox."
  type        = string
  default     = "germanywestcentral"
}

variable "operator_object_id" {
  description = "Object ID of the person who runs Terraform from their Mac. Gets Key Vault Secrets Officer, so a local apply can write the secrets."
  type        = string
}

variable "github_oidc_subject_prefix" {
  description = "sub_claim_prefix of the repository's OIDC subject, from `gh api repos/<owner>/<repo>/actions/oidc/customization/sub`."
  type        = string
}

variable "cloudflare_zone_id" {
  description = "Zone that sandbox hostnames live under. From the `skybox` 1Password vault through secrets.env."
  type        = string
}

variable "cloudflare_api_token" {
  description = "Cloudflare API token. From the `skybox` 1Password vault through secrets.env."
  type        = string
  sensitive   = true
}

variable "claude_code_oauth_token" {
  description = "Claude Code OAuth token that sandboxes sign in with. From the `skybox` 1Password vault through secrets.env."
  type        = string
  sensitive   = true
}

variable "cloudflare_account_id" {
  description = "Account that holds the sandbox tunnels and the Access application. From the `skybox` 1Password vault through secrets.env."
  type        = string
}

variable "cloudflare_zone" {
  description = "Name of the zone that sandbox hostnames live under. From the `skybox` 1Password vault through secrets.env."
  type        = string
}

variable "access_email" {
  description = "Email of the only Cloudflare Access identity that may reach a sandbox. From the `skybox` 1Password vault through secrets.env."
  type        = string
  sensitive   = true
}

variable "control_plane_image" {
  description = "Image the control plane's Container Apps Job runs. CI publishes it from master."
  type        = string
  default     = "ghcr.io/richardamare/skyboxctl:master"
}

variable "control_plane_enforce" {
  description = "Whether the control plane acts. Without it every run only logs what it would do."
  type        = bool
  default     = false
}
