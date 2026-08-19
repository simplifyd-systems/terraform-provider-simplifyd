# Connections are identified by <workspace>/<project>/<env>/<gateway>/<connection>.
terraform import simplifyd_ipsec_connection.partner 0192f3a1-6c4e-7a10-9b2d-3f8c1a5e7b04/0192f3a1-8d21-7c33-af14-6b90e2d45c77/0192f3a1-9e55-7f08-b3c6-1d47a8e90f21/partner-vpn/0192f3a2-44c1-7b90-9d1e-5c8b2f0a7d63

# The pre-shared key is never returned by the API, so an imported connection has
# no psk in state. Set it in configuration and apply to rotate it.
