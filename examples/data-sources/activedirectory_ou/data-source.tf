# Look up a single organizational unit by GUID or distinguished name. Exactly
# one identity attribute must be set; the lookup errors if the OU is absent.
data "activedirectory_ou" "staff" {
  dn = "OU=Staff,DC=corp,DC=local"
}

output "staff_guid" {
  value = data.activedirectory_ou.staff.id
}
