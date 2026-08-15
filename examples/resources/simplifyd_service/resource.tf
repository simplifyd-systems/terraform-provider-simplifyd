# A container image deployed from a registry.
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

# A managed datastore. Its spec lives in a per-type block.
resource "simplifyd_service" "db" {
  name = "orders-db"
  type = "postgres"

  postgres = {
    storage_gb = 20
    mode       = "replication"
  }
}

# Staged, not rolled out: changes land in the service's changeset and wait for
# an explicit deploy.
resource "simplifyd_service" "worker" {
  name   = "worker"
  type   = "docker"
  deploy = false

  docker = {
    image = "image-hub.simplifyd.dev/cloud/storefront-worker"
    tag   = "v1.4.2"
  }
}
