# Terraform Provider for Simplifyd Cloud

Manage Simplifyd Cloud projects, environments, and services declaratively.
Built on [terraform-plugin-framework][fw] and the [cloud-go-sdk][sdk].
Works with both Terraform and OpenTofu.

[fw]: https://github.com/hashicorp/terraform-plugin-framework
[sdk]: https://github.com/simplifyd-systems/cloud-go-sdk

## Usage

```hcl
provider "simplifyd" {
  workspace = "acme"      # or SIMPLIFYD_WORKSPACE
  project   = "storefront" # or SIMPLIFYD_PROJECT
  env       = "production" # or SIMPLIFYD_ENV
  # api_token from SIMPLIFYD_API_TOKEN
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

**Scope defaults.** The Simplifyd API is path-scoped
(`/workspaces/:ws/projects/:proj/envs/:env/...`). Rather than repeat those three
slugs on every resource, the provider block carries defaults that any resource
may override — so one config can span multiple projects when it needs to.

**Composite IDs.** Simplifyd identifies objects by slug, unique only within the
parent scope. Terraform needs one opaque string, so IDs are the full path:
`<workspace>/<project>/<env>/<service>`. That is also the `terraform import`
format.

**Deploys are synchronous.** `simplifyd_service` has a `deploy` flag (default
`true`). When set, create and update approve the pending changeset, roll out,
and block until the deployment reaches a terminal state — a failed rollout
fails the apply rather than silently leaving a broken service. Set
`deploy = false` to stage changes into the changeset without rolling out.

**Destroy is real, and owner-only.** `simplifyd_project` and
`simplifyd_environment` call the API's delete endpoints, which tear down every
service underneath them synchronously. Both require the workspace `owner` role,
so a project token (`sk_proj_*`) cannot destroy either — CI that plans and
applies with a project token must not be the thing that runs `destroy`.

**A project keeps its last environment.** The API refuses to delete a project's
only environment with a 409. Terraform destroys an environment before the
project containing it, so a config declaring both would otherwise be impossible
to destroy; `simplifyd_environment`'s `Delete` treats that one refusal as
success with a warning, on the basis that the project teardown immediately
after removes the environment anyway. If only the environment is being
destroyed, the warning is the signal that it is still there.

**Action-based updates.** The API's service PATCH mutates one concern per
request (`name`, `vcpus`, `image`, …), so a single Terraform update can fan out
into several calls. See `applyUpdates` in `internal/provider/resource_service.go`.

## Known gaps

These are API limitations, not provider bugs. Each needs a `cloudapi` change:

| Gap | Effect |
|---|---|
| Config/ingress/variable have no per-object `GET` | `Read` fetches the parent service and filters — works, but is chattier than it needs to be |
| No workspace-scoped machine tokens | Tokens are project-scoped (`sk_proj_*`); CI for a multi-project config needs one token per project |

Not yet implemented, in rough priority order: `simplifyd_private_access_grant`,
`simplifyd_project_token`, `simplifyd_workspace_member`, `simplifyd_env_variable`
(environment-level shared variables), and probe configuration on
`simplifyd_service`.

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
