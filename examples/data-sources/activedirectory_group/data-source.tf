# Look up a single group by GUID, DN, SID, or sAMAccountName. Exactly one
# identity attribute must be set; the lookup errors if the group is absent.
data "activedirectory_group" "admins" {
  sam_account_name = "Domain Admins"
}

output "admins_dn" {
  value = data.activedirectory_group.admins.dn
}
