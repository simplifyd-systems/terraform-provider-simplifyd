# Read a service this configuration does not manage — to wire a new service to
# an existing database, for example.
data "simplifyd_service" "existing_db" {
  slug = "orders-db"
}

resource "simplifyd_service_variable" "database_url" {
  service = simplifyd_service.api.slug
  name    = "DATABASE_URL"
  value   = "postgres://app@${data.simplifyd_service.existing_db.private_hostname}:5432/orders"
}
