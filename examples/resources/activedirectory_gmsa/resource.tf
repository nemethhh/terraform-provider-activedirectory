# Prerequisite: a KDS root key must exist in the forest before any gMSA can
# be created. Windows Server 2025 domain controllers auto-provision one on
# the first gMSA creation; against older DCs an administrator must run
# Add-KdsRootKey first, and the key needs up to 10 hours to propagate before
# a gMSA create against it will succeed.
#
# Active Directory generates and rotates this account's password itself —
# unlike activedirectory_user, there is no `password` attribute here at all.
resource "activedirectory_gmsa" "web01" {
  name         = "svc-web01"
  container    = activedirectory_ou.staff.dn
  dns_hostname = "web01-svc.corp.local"

  service_principal_names = [
    "HTTP/web01-svc.corp.local",
    "HTTP/web01-svc",
  ]

  # Only these computers/groups may retrieve the managed password. Full-
  # replace: setting this — including to [] — replaces AD's entire set.
  principals_allowed_to_retrieve_managed_password = [
    activedirectory_group.developers.id,    # a group of member servers
    "9f2c8f1e-1234-4000-8000-000000000004", # e.g. one computer object
  ]

  kerberos_encryption_type = ["AES128", "AES256"]
}
