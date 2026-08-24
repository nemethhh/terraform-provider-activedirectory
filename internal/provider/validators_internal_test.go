package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// runStringValidators runs every validator in vs against value and reports
// whether any of them produced an error diagnostic. There is no gMSA
// resource yet (that is Task 8) to drive gmsaSamAccountNameValidators through
// the resource.UnitTest harness the way TestSamAccountNameValidatorRejectsBadValues
// does in validators_test.go, so this calls ValidateString directly.
func runStringValidators(vs []validator.String, value string) bool {
	req := validator.StringRequest{
		Path:        path.Root("sam_account_name"),
		ConfigValue: types.StringValue(value),
	}
	for _, v := range vs {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), req, resp)
		if resp.Diagnostics.HasError() {
			return true // invalid
		}
	}
	return false // valid
}

// TestGMSASamValidator pins the gMSA sam_account_name ceiling at 15 (the
// NetBIOS/down-level logon name limit for a computer-like account) plus the
// same illegal-character/no-trailer rule every other sAMAccountName validator
// shares.
func TestGMSASamValidator(t *testing.T) {
	v := gmsaSamAccountNameValidators()
	cases := []struct {
		name      string
		sam       string
		wantError bool
	}{
		{"15 chars ok", "abcdefghij12345", false},
		{"16 chars rejected", "abcdefghij123456", true},
		{"illegal char rejected", "bad*name", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotErr := runStringValidators(v, tc.sam)
			if gotErr != tc.wantError {
				t.Errorf("gmsaSamAccountNameValidators() on %q: error=%v, want %v", tc.sam, gotErr, tc.wantError)
			}
		})
	}
}

// TestKerberosEncryptionTypeValues pins the exact set and order the gMSA
// resource's kerberos_encryption_type validator (Task 8) is built from.
func TestKerberosEncryptionTypeValues(t *testing.T) {
	want := []string{"None", "DES", "RC4", "AES128", "AES256"}
	got := kerberosEncryptionTypeValues()
	if len(got) != len(want) {
		t.Fatalf("kerberosEncryptionTypeValues() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kerberosEncryptionTypeValues()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
