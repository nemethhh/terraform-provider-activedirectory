resource "activedirectory_group" "developers" {
  name             = "Developers"
  sam_account_name = "developers"
  container        = activedirectory_ou.staff.dn
  scope            = "global"
  category         = "security"
  description      = "Everyone who writes code"
}
