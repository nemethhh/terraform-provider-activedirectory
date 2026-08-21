# Search for users under a container. filter_by terms are escaped for you and
# ANDed together; a raw ldap_filter is ANDed on top. Exceeding max_results is an
# error, never a silently truncated set.
data "activedirectory_users" "sales" {
  container = "OU=Sales,DC=corp,DC=local"
  scope     = "subtree"
  filter_by = {
    department = "Sales"
  }
}

output "sales_upns" {
  value = [for u in data.activedirectory_users.sales.users : u.user_principal_name]
}
