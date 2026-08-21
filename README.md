# Terraform Provider for Simplifyd Cloud

Manage Simplifyd Cloud environments and services declaratively.
Built on [terraform-plugin-framework][fw] and the [cloud-go-sdk][sdk].
Works with both Terraform and OpenTofu.

[fw]: https://github.com/hashicorp/terraform-plugin-framework
[sdk]: https://github.com/simplifyd-systems/cloud-go-sdk

## Usage

```hcl
# api_token comes from SIMPLIFYD_API_TOKEN and must be a project token
# (sk_proj_*). Everything else is derived from it.
provider "simplifyd" {
  env = "production" # or SIMPLIFYD_ENV; omit for an env-scoped token
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
```

See [`examples/provider`](examples/provider) for a fuller configuration.

## Design notes

**Composite IDs.** Simplifyd identifies objects by slug, unique only within the
parent scope. Terraform needs one opaque string, so IDs are the full path:
`<workspace>/<project>/<env>/<service>`. That is also the `terraform import`
format.

**`deploy = false` leaves the service reading back its pre-deploy values.**
`vcpus`, `memory` and the other action-based fields are staged into the
changeset, not written to the service, so plans keep showing the configured
values as a diff until a deploy applies them. That is the platform's model, not
drift the provider can resolve.

**A rollout the platform never reports on times out.** Deployment status is
written by a watch on the controller's Service CR, so a type whose upstream
operator is absent from the target cluster leaves the status empty forever. The
wait is capped at 15 minutes and fails with an explanation rather than hanging
the apply; `deploy = false` stages the change without waiting.

**Deploys are synchronous.** `simplifyd_service` has a `deploy` flag (default
`true`). When set, create and update approve the pending changeset, roll out,
and block until the deployment reaches a terminal state — a failed rollout
fails the apply rather than silently leaving a broken service. Set
`deploy = false` to stage changes into the changeset without rolling out.

**Destroy is real.** `simplifyd_environment` calls the API's delete endpoint,
which tears down every service underneath it synchronously.

**No project resource.** Creating or destroying a project is a workspace-level
action, and a project token has no authority above its own project. Create
projects in the console (or with a workspace-level credential) and point a
token at them. For the same reason the API refuses to delete a project's only
environment with a 409, and the provider surfaces that as an error: there is
nothing it can do about it.

**Kafka is create-only.** The API exposes no update action for a Kafka
service's pools, storage or version — storage is immutable and a version change
needs a multi-step rolling procedure the platform will not perform implicitly —
so every field of the `kafka` block forces replacement rather than silently
diverging from the running cluster.

**Write-only secrets.** `simplifyd_ipsec_connection.psk` is never returned by
the API, so it is tracked from state alone and a changed value is applied as a
key rotation. The same applies to `simplifyd_service_variable.value`. Source
both from a secret store, not from literals.

**Gateway routes and tunnels need a deploy.** `simplifyd_gateway_route` and
`simplifyd_ipsec_connection` write configuration that the running pod picks up
on its next deployment, so a change to either is live only once the gateway
service is redeployed.

**The create endpoint names the managed datastores itself.** It stamps "Kafka",
"Postgres" or "Redis" over whatever `name` the request carried, so the provider
follows a create with a name patch. Without it every plan after the first showed
the configured name as drift.

**Per-type blocks are gated on the service's type, not on their presence.** The
API serializes a zero-valued `kafka_svc`, `redis_svc` and so on for every
service whatever its type, so a docker service reads back carrying an empty
Kafka block. Trusting the pointer wrote that block into state and forced
replacement on the next plan.

**Action-based updates.** The API's service PATCH mutates one concern per
request (`name`, `vcpus`, `image`, …), so a single Terraform update can fan out
into several calls. See `applyUpdates` in `internal/provider/resource_service.go`.


## Development

```bash
make build            # build the binary
make test             # unit tests (no API access needed)
make testacc          # acceptance tests — point at STAGING, they create real resources
make docs             # regenerate docs/ with tfplugindocs
```

To run against a local build, add to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "simplifyd-systems/simplifyd" = "/Users/you/go/bin"
  }
  direct {}
}
```

With `dev_overrides` active, skip `terraform init` and run `terraform plan`
directly.

## Releasing

Tag `vX.Y.Z` and push. The `release` workflow runs GoReleaser, which produces
the archives and the GPG-signed `SHA256SUMS` the Terraform Registry requires.
Repository secrets needed: `GPG_PRIVATE_KEY`, `PASSPHRASE`.
