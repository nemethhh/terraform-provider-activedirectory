resource "activedirectory_ou" "staff" {
  name        = "Staff"
  container   = "DC=corp,DC=local"
  description = "Everyone on the payroll"
}

# Referencing dn is what makes this a real dependency edge rather than a
# hardcoded string.
resource "activedirectory_ou" "contractors" {
  name      = "Contractors"
  container = activedirectory_ou.staff.dn
}
