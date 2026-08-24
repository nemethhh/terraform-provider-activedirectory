# Search for computers under a container by scope and filter. filter_by
# terms are escaped for you and ANDed together; a raw ldap_filter is ANDed
# on top, so you own its correctness.
data "activedirectory_computers" "servers" {
  container   = "OU=Servers,DC=corp,DC=local"
  scope       = "subtree"
  ldap_filter = "(operatingSystem=*Server*)"
}

output "server_hostnames" {
  value = [for c in data.activedirectory_computers.servers.computers : c.dns_hostname]
}
