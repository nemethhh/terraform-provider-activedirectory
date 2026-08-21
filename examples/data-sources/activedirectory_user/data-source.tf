# Look up a single user by GUID, DN, SID, or sAMAccountName. Exactly one
# identity attribute must be set; the lookup errors if the user is absent.
data "activedirectory_user" "jdoe" {
  sam_account_name = "jdoe"
}

output "jdoe_upn" {
  value = data.activedirectory_user.jdoe.user_principal_name
}
