# By distinguished name…
terraform import activedirectory_ou.staff "OU=Staff,DC=corp,DC=local"
# …or by objectGUID. Either resolves to the GUID in state.
terraform import activedirectory_ou.staff "9f2c8f1e-1234-4000-8000-000000000001"
