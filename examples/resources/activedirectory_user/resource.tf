# The password is write-only: it never reaches state or a plan file. Rotate it
# by incrementing password_version.
ephemeral "random_password" "jdoe" {
  length = 24
}

resource "activedirectory_user" "jdoe" {
  sam_account_name    = "jdoe"
  container           = activedirectory_ou.staff.dn
  user_principal_name = "jdoe@corp.local"
  display_name        = "John Doe"
  given_name          = "John"
  surname             = "Doe"
  enabled             = true

  password         = ephemeral.random_password.jdoe.result
  password_version = 1

  # An attribute a sync engine also writes would otherwise churn forever.
  lifecycle {
    ignore_changes = [display_name]
  }
}
