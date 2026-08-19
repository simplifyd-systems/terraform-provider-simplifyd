resource "simplifyd_service" "gateway" {
  name = "edge"
  type = "http_gateway"
}

# Longest/most specific prefixes want the higher priority.
resource "simplifyd_gateway_route" "api" {
  service      = simplifyd_service.gateway.slug
  path_prefix  = "/api"
  backend_slug = simplifyd_service.api.slug
  backend_port = 8080
  priority     = 10
}

# strip_prefix hands the backend "/health" rather than "/api/v2/health".
resource "simplifyd_gateway_route" "api_v2" {
  service      = simplifyd_service.gateway.slug
  path_prefix  = "/api/v2"
  backend_slug = simplifyd_service.api_v2.slug
  backend_port = 8080
  strip_prefix = true
  priority     = 20
}
