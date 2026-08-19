resource "simplifyd_service" "vpn" {
  name = "partner-vpn"
  type = "ipsec_gateway"
}

# The counterparty configures simplifyd_service.vpn.ipsec_gateway.public_ip as
# their peer address — it is fixed for the gateway's life.
resource "simplifyd_ipsec_connection" "partner" {
  service        = simplifyd_service.vpn.slug
  name           = "partner-a"
  remote_gateway = "203.0.113.10"
  remote_subnets = ["192.168.50.0/24"]

  # Counterparties routinely mandate an exact algorithm set.
  ike_proposal = "aes256-sha256-modp2048"
  esp_proposal = "aes256-sha256-modp2048"

  # Write-only at the API, so it lives in state: source it from a secret store,
  # not a literal. Changing it rotates the key on the next gateway deployment.
  psk = var.partner_psk
}
