terraform {
  required_providers {
    simplifyd = {
      source  = "simplifyd-systems/simplifyd"
      version = "~> 0.1"
    }
  }
}

provider "simplifyd" {
  # api_token is read from SIMPLIFYD_API_TOKEN. It must be a project token
  # (sk_proj_*), which already identifies the workspace and project.
  env = "production" # omit when the token is scoped to one environment
}

variable "image_tag" {
  description = "Image tag to deploy."
  type        = string
  default     = "latest"
}

variable "db_password" {
  description = "Password the API uses to connect to Postgres."
  type        = string
  sensitive   = true
}

# ── Postgres ──────────────────────────────────────────────────────────────────

resource "simplifyd_service" "db" {
  name = "orders-db"
  type = "postgres"

  postgres = {
    storage_gb = 20
    mode       = "standalone"
  }
}

# ── API ───────────────────────────────────────────────────────────────────────

resource "simplifyd_service" "api" {
  name   = "api"
  type   = "docker"
  vcpus  = 1
  memory = 1024

  docker = {
    image = "image-hub.simplifyd.dev/cloud/storefront-api"
    tag   = var.image_tag
  }
}

resource "simplifyd_service_variable" "database_url" {
  service = simplifyd_service.api.slug
  name    = "DATABASE_URL"

  # private_hostname is reachable from any service in the same project.
  value = "postgres://app:${var.db_password}@${simplifyd_service.db.private_hostname}:5432/orders?sslmode=require"
}

resource "simplifyd_service_variable" "log_level" {
  service = simplifyd_service.api.slug
  name    = "LOG_LEVEL"
  value   = "info"
}

resource "simplifyd_ingress" "api_http" {
  service  = simplifyd_service.api.slug
  protocol = "HTTP"
  port     = 8080
}

output "api_url" {
  value = "https://${simplifyd_ingress.api_http.vanity_fqdn}"
}

output "db_hostname" {
  value = simplifyd_service.db.private_hostname
}
