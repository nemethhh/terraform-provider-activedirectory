# Authoritative: this resource owns the group's entire membership. Any member not
# listed here is removed on apply — do not also use activedirectory_group_member
# on the same group.
resource "activedirectory_group_membership" "devs" {
  group_id = activedirectory_group.devs.id
  members = [
    activedirectory_user.alice.id,
    activedirectory_user.bob.id,
  ]
}
