# HTTP ingress gets a vanity FQDN, plus any custom domain you point at it.
resource "simplifyd_ingress" "api_http" {
  service     = simplifyd_service.api.slug
  protocol    = "HTTP"
  port        = 8080
  custom_fqdn = "api.acme.com"
}

# TCP and UDP ingress is published on a shared IP at a random public port.
resource "simplifyd_ingress" "db_tcp" {
  service  = simplifyd_service.db.slug
  protocol = "TCP"
  port     = 5432

  # Without an allowlist this port is reachable from anywhere on the internet.
  allowed_source_ranges = ["102.221.184.0/22", "10.0.0.5"]
}
