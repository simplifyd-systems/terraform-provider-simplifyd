# Services are identified by <workspace>/<project>/<env>/<service>.
terraform import simplifyd_service.api 0192f3a1-6c4e-7a10-9b2d-3f8c1a5e7b04/0192f3a1-8d21-7c33-af14-6b90e2d45c77/0192f3a1-9e55-7f08-b3c6-1d47a8e90f21/api

# Slugs are UUIDs; the workspace and project must be the ones the API token
# is scoped to. Copy them from the resource's URL in the Simplifyd console.
