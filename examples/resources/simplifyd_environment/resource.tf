resource "simplifyd_environment" "staging" {
  project = simplifyd_project.storefront.slug
  name    = "staging"
}
