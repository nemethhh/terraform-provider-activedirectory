# A pre-staged computer account for a machine that will later join the
# domain. Active Directory generates and rotates this account's own password
# after domain join — like activedirectory_gmsa, this resource has no
# password attribute at all.
resource "activedirectory_computer" "web01" {
  name        = "web01"
  container   = activedirectory_ou.servers.dn
  description = "Web tier, app team owned"
  enabled     = true

  dns_hostname = "web01.corp.local"

  service_principal_names = [
    "HTTP/web01.corp.local",
    "HTTP/web01",
  ]

  # --- Delegation (uncomment as needed) --------------------------------
  #
  # Resource-based constrained delegation (RBCD): only these principals may
  # delegate to this account (PrincipalsAllowedToDelegateToAccount). Values
  # are objectGUIDs. Full-replace: setting this — including to [] —
  # replaces AD's entire set.
  # principals_allowed_to_delegate_to_account = [
  #   activedirectory_computer.gateway.id,    # e.g. a front-end proxy
  #   "9f2c8f1e-1234-4000-8000-000000000004",
  # ]
  #
  # Constrained delegation: the service principal names this account may
  # delegate to (msDS-AllowedToDelegateTo). Full-replace, the same as
  # service_principal_names. Setting this (like trusted_for_delegation)
  # requires the Terraform-running account to hold SeEnableDelegationPrivilege.
  # allowed_to_delegate_to = [
  #   "HTTP/backend01.corp.local",
  # ]
  #
  # The Kerberos encryption types this account supports. Defaults to
  # whatever Active Directory assigns a newly created computer.
  # kerberos_encryption_type = ["AES128", "AES256"]
}
