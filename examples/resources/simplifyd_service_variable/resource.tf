resource "simplifyd_service_variable" "database_url" {
  service = simplifyd_service.api.slug
  name    = "DATABASE_URL"
  value   = "postgres://app@${simplifyd_service.db.private_hostname}:5432/orders"
}
