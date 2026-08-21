# Search for groups under a container. A raw ldap_filter is passed through
# verbatim, so you own its correctness; filter_by is the escaped, friendlier
# path for simple equality.
data "activedirectory_groups" "app_groups" {
  container   = "OU=Groups,DC=corp,DC=local"
  scope       = "subtree"
  ldap_filter = "(name=app-*)"
}

output "app_group_dns" {
  value = [for g in data.activedirectory_groups.app_groups.groups : g.dn]
}
