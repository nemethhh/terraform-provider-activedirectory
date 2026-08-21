# Non-authoritative: manage a single membership edge. Other members of the group
# are left untouched.
resource "activedirectory_group_member" "alice_in_devs" {
  group_id  = activedirectory_group.devs.id
  member_id = activedirectory_user.alice.id
}
