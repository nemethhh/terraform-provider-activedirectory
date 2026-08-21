# List a group's direct members. Identify the group the same way the singular
# group data source does — by GUID, DN, SID, or sAMAccountName.
data "activedirectory_group_members" "admins" {
  sam_account_name = "Domain Admins"
}

output "admin_member_dns" {
  value = [for m in data.activedirectory_group_members.admins.members : m.dn]
}
