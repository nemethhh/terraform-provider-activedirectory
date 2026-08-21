# The primitive: one explicit, non-authoritative ACE. This grants the
# helpdesk group ExtendedRight "Reset Password" over every user beneath
# OU=Staff, without taking ownership of the OU's DACL as a whole -- any other
# ACE on the same object, inherited or explicit, is left untouched.
resource "activedirectory_access_rule" "helpdesk_reset_pw" {
  target      = activedirectory_ou.staff.dn       # any object, DN or GUID
  trustee     = activedirectory_group.helpdesk.id # any security principal -> SID
  rights      = ["ExtendedRight"]                 # AD rights names, not hex
  object_type = "Reset Password"                  # friendly name or GUID; "" = all
  applies_to = {
    scope        = "descendants" # this | descendants | children
    object_class = "user"        # inheritedObjectType: name, GUID, or ""
  }
  type = "Allow" # Allow | Deny (default Allow)
}

# The delegation-template fan-out: activedirectory_delegation_template expands
# a curated task into concrete rules at plan time (pure computation, no
# directory read), fed into the primitive with for_each so each rule becomes
# its own access_rule instance.
data "activedirectory_delegation_template" "manage_users" {
  task = "manage_users"
}

resource "activedirectory_access_rule" "manage_users" {
  for_each = {
    for idx, r in data.activedirectory_delegation_template.manage_users.rules : tostring(idx) => r
  }
  target      = activedirectory_ou.staff.dn
  trustee     = activedirectory_group.admins.id
  rights      = each.value.rights
  object_type = each.value.object_type
  applies_to  = each.value.applies_to
  type        = each.value.type
}
