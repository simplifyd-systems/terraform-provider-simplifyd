resource "simplifyd_project" "storefront" {
  name = "storefront"
}

# Projects are created with one environment already in place. Manage additional
# ones with simplifyd_environment.
