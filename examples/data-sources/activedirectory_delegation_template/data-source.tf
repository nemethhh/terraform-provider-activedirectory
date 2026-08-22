# Delegation templates expand a curated task into concrete access-control
# rules at plan time -- pure computation, no directory read -- so the result
# is known before apply and can drive for_each on activedirectory_access_rule.
data "activedirectory_delegation_template" "reset_user_passwords" {
  task = "reset_user_passwords"
}

data "activedirectory_delegation_template" "manage_users" {
  task = "manage_users"
}

data "activedirectory_delegation_template" "modify_group_membership" {
  task = "modify_group_membership"
}

data "activedirectory_delegation_template" "manage_groups" {
  task = "manage_groups"
}

output "reset_user_passwords_rules" {
  value = data.activedirectory_delegation_template.reset_user_passwords.rules
}

output "manage_users_rules" {
  value = data.activedirectory_delegation_template.manage_users.rules
}

output "modify_group_membership_rules" {
  value = data.activedirectory_delegation_template.modify_group_membership.rules
}

output "manage_groups_rules" {
  value = data.activedirectory_delegation_template.manage_groups.rules
}
