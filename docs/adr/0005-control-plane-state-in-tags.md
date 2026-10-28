# The control plane keeps its state in Azure tags, not a database

`skyboxctl` runs as a scheduled Azure Container Apps Job that reconciles idempotently. It reads every sandbox's state from `skybox:` tags on Azure resources and from the Cloudflare API, and decides each run from that state alone, so a crashed run is simply repeated by the next one. A database was rejected because it would be a second source of truth that drifts from what Azure and Cloudflare actually hold, and it would cost money while idle. The control plane touches only resources with a `skybox:` tag, and starts in dry-run.

Static Cloudflare parts live in Terraform. Per-sandbox tunnels and DNS records do not: the CLI creates them through the Cloudflare API, so `skybox up` never runs `terraform apply`, and the control plane removes whatever a failed `skybox down` leaves behind.
