terraform {
  required_providers {
    simplifyd = {
      source  = "simplifyd-systems/simplifyd"
      version = "~> 0.1"
    }
  }
}

provider "simplifyd" {
  # api_token sourced from SIMPLIFYD_API_TOKEN
  workspace = "acme"
  project   = "storefront"
  env       = "production"
}

resource "simplifyd_service" "api" {
  name   = "api"
  type   = "docker"
  vcpus  = 1
  memory = 1024

  docker = {
    image = "image-hub.simplifyd.dev/cloud/storefront-api"
    tag   = "v1.4.2"
  }
}

resource "simplifyd_service" "db" {
  name = "orders-db"
  type = "postgres"

  postgres = {
    storage_gb = 20
    mode       = "replication"
  }
}

resource "simplifyd_service_variable" "database_url" {
  service = simplifyd_service.api.slug
  name    = "DATABASE_URL"
  value   = "postgres://app@${simplifyd_service.db.private_hostname}:5432/orders"
}

resource "simplifyd_service_config" "app_config" {
  service    = simplifyd_service.api.slug
  name       = "app-config"
  mount_path = "/etc/storefront/config.yaml"

  # $${{...}} escapes HCL so the platform interpolates it at deploy time.
  content = <<-YAML
    environment: $${{ENVIRONMENT}}
    service: $${{SERVICE}}
    log_level: info
  YAML
}

resource "simplifyd_ingress" "api_http" {
  service     = simplifyd_service.api.slug
  protocol    = "HTTP"
  port        = 8080
  custom_fqdn = "api.acme.com"
}

resource "simplifyd_ingress" "db_tcp" {
  service  = simplifyd_service.db.slug
  protocol = "TCP"
  port     = 5432

  # Without an allowlist this port is reachable from anywhere.
  allowed_source_ranges = ["102.221.184.0/22", "10.0.0.5"]
}

output "api_url" {
  value = "https://${simplifyd_ingress.api_http.vanity_fqdn}"
}
