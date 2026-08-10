package provider

import "testing"

// terraform import activedirectory_user.jdoe jdoe must work as well as
// importing by GUID, so the form is detected rather than demanded.
func TestIdentityFromImportID(t *testing.T) {
	tests := []struct{ in, wantForm, wantArg string }{
		{"9f2c8f1e-1234-4000-8000-000000000001", "guid", "9f2c8f1e-1234-4000-8000-000000000001"},
		{"CN=jdoe,OU=Staff,DC=corp,DC=local", "dn", "CN=jdoe,OU=Staff,DC=corp,DC=local"},
		{"OU=Staff,DC=corp,DC=local", "dn", "OU=Staff,DC=corp,DC=local"},
		{"S-1-5-21-1-2-3-1104", "sid", "S-1-5-21-1-2-3-1104"},
		{"jdoe", "sam", "jdoe"},
		{`CORP\jdoe`, "sam", `CORP\jdoe`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := identityFromImportID(tt.in)
			if got.String() != tt.wantForm+":"+tt.wantArg {
				t.Errorf("identityFromImportID(%q) = %s, want %s:%s", tt.in, got, tt.wantForm, tt.wantArg)
			}
		})
	}
}
