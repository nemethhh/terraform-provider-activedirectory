# Search for organizational units under a container. A one_level scope lists the
# immediate children of the search base; subtree (the default) walks the whole
# tree beneath it.
data "activedirectory_ous" "top" {
  container = "DC=corp,DC=local"
  scope     = "one_level"
}

output "top_level_ou_dns" {
  value = [for o in data.activedirectory_ous.top.ous : o.dn]
}
