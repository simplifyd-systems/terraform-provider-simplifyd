resource "simplifyd_service_config" "app_config" {
  service    = simplifyd_service.api.slug
  name       = "app-config"
  mount_path = "/etc/storefront/config.yaml"

  # $${{...}} escapes HCL's own interpolation so the platform substitutes these
  # at deploy time. SERVICE, ENVIRONMENT and PROJECT are always available.
  content = <<-YAML
    environment: $${{ENVIRONMENT}}
    service: $${{SERVICE}}
    log_level: info
  YAML
}
