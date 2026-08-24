# Look up a single computer account by GUID, DN, SID, or sAMAccountName.
# Exactly one identity attribute must be set; the lookup errors if the
# computer is absent.
data "activedirectory_computer" "web01" {
  sam_account_name = "web01"
}

output "web01_dns_hostname" {
  value = data.activedirectory_computer.web01.dns_hostname
}

# Alternative lookups:
#
# data "activedirectory_computer" "web01" {
#   guid = "9f2c8f1e-1234-4000-8000-000000000004"
# }
#
# data "activedirectory_computer" "web01" {
#   dn = "CN=web01,OU=Servers,DC=corp,DC=local"
# }
