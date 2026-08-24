# Look up a single gMSA by GUID, DN, SID, or sAMAccountName. Exactly one
# identity attribute must be set; the lookup errors if the gMSA is absent.
data "activedirectory_gmsa" "web01" {
  sam_account_name = "svc-web01"
}

output "web01_dns_hostname" {
  value = data.activedirectory_gmsa.web01.dns_hostname
}
