package provider

import "testing"

func TestDNEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "OU=Staff,DC=corp,DC=local", "OU=Staff,DC=corp,DC=local", true},
		{"attribute type case", "ou=Staff,dc=corp,dc=local", "OU=Staff,DC=corp,DC=local", true},
		{"value case", "OU=STAFF,DC=CORP,DC=LOCAL", "OU=staff,DC=corp,DC=local", true},
		{"separator spacing", "OU=Staff, DC=corp, DC=local", "OU=Staff,DC=corp,DC=local", true},
		{"different name", "OU=Sales,DC=corp,DC=local", "OU=Staff,DC=corp,DC=local", false},
		{"different depth", "OU=Staff,DC=corp,DC=local", "OU=Staff,OU=HQ,DC=corp,DC=local", false},
		// An escaped comma is part of one RDN, so these are two-component DNs
		// that differ only in case, not four-component DNs that happen to line
		// up. Splitting on every comma is what gets this wrong.
		{"escaped comma", `OU=Sales\, EMEA,DC=corp,DC=local`, `ou=sales\, emea,dc=corp,dc=local`, true},
		{"escaped comma differing", `OU=Sales\, EMEA,DC=corp,DC=local`, `OU=Sales\, APAC,DC=corp,DC=local`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dnEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("dnEqual(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSplitDNKeepsAnEscapedCommaTogether(t *testing.T) {
	got := splitDN(`OU=Sales\, EMEA,DC=corp,DC=local`)
	want := []string{`OU=Sales\, EMEA`, "DC=corp", "DC=local"}
	if len(got) != len(want) {
		t.Fatalf("splitDN = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("component %d = %q, want %q", i, got[i], want[i])
		}
	}
}
