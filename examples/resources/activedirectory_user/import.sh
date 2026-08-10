# By distinguished name…
terraform import activedirectory_user.jdoe "CN=jdoe,OU=Staff,DC=corp,DC=local"
# …or by objectGUID. Either resolves to the GUID in state.
terraform import activedirectory_user.jdoe "9f2c8f1e-1234-4000-8000-000000000003"
# The brownfield case: adopt an account by the name people actually know it by.
terraform import activedirectory_user.jdoe jdoe
