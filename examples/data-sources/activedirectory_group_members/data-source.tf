# List a group's direct members. Identify the group the same way the singular
# group data source does — by GUID, DN, SID, or sAMAccountName.
data "activedirectory_group_members" "admins" {
  sam_account_name = "Domain Admins"
}

output "admin_member_dns" {
  value = [for m in data.activedirectory_group_members.admins.members : m.dn]
}

# Set recursive = true for effective membership: every leaf user/computer
# reachable through nested groups, flattened. The nested group objects
# themselves are not returned.
data "activedirectory_group_members" "admins_effective" {
  sam_account_name = "Domain Admins"
  recursive        = true
}

output "admin_effective_member_dns" {
  value = [for m in data.activedirectory_group_members.admins_effective.members : m.dn]
}
