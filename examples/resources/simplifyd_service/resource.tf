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

# Tuned server settings on a managed datastore. The map is applied whole, so
# removing an entry restores that setting's platform default.
resource "simplifyd_service" "reporting_db" {
  name = "reporting-db"
  type = "postgres"

  postgres = {
    storage_gb = 50

    # Only the platform's allowlist is accepted — see the provider error for
    # the full set. shared_buffers and effective_cache_size are derived from
    # the service's memory and are deliberately not settable.
    parameters = {
      work_mem                        = "16MB"
      maintenance_work_mem            = "256MB"
      statement_timeout               = "30s"
      max_parallel_workers_per_gather = "4"
    }
  }
}

# Kafka in KRaft mode. Cluster mode splits brokers and controllers into separate
# pools; every node is billed, and controllers carry a fixed 10 GB each.
resource "simplifyd_service" "events" {
  name = "events"
  type = "kafka"

  kafka = {
    mode        = "cluster"
    brokers     = 3
    controllers = 3
    storage_gb  = 50
  }
}

# A site-to-site VPN gateway. Tunnels are separate resources; the gateway itself
# is billed per tunnel-minute, so it takes no vcpus/memory/replicas.
resource "simplifyd_service" "vpn" {
  name = "partner-vpn"
  type = "ipsec_gateway"

  ipsec_gateway = {
    local_subnets = ["10.42.0.0/16"]
  }
}
