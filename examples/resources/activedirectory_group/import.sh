# By distinguished name…
terraform import activedirectory_group.developers "CN=Developers,OU=Staff,DC=corp,DC=local"
# …or by objectGUID. Either resolves to the GUID in state.
terraform import activedirectory_group.developers "9f2c8f1e-1234-4000-8000-000000000002"
# A group is a security principal, so its sAMAccountName works too.
terraform import activedirectory_group.developers developers
