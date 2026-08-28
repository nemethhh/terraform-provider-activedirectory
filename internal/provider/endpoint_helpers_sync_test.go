package provider_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// The embedded ACL helper block in New-AdProviderEndpoint.ps1 must stay
// byte-identical (modulo line endings / trailing whitespace) to go-adpwsh's
// authoritative copy. If go-adpwsh changes the helpers, re-embed them here.
func TestEndpointHelpersInSyncWithLibrary(t *testing.T) {
	raw, err := os.ReadFile("../../scripts/host/New-AdProviderEndpoint.ps1")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)# >>> go-adpwsh ACL endpoint helpers.*?>>>\r?\n(.*?)\r?\n\s*# <<< go-adpwsh ACL endpoint helpers <<<`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatal("could not find the sentinel-delimited ACL helper block in New-AdProviderEndpoint.ps1")
	}
	// The embedded block assigns `$aclFunctionDefinitions = <literal>`; strip the
	// assignment prefix so only the literal is compared.
	embedded := strings.TrimSpace(string(m[1]))
	embedded = strings.TrimPrefix(embedded, "$aclFunctionDefinitions =")
	embedded = norm(embedded)
	want := norm(adpwsh.ACLEndpointHelpers())
	if embedded != want {
		t.Fatalf("embedded ACL helpers drifted from go-adpwsh's ACLEndpointHelpers().\nRe-copy the literal into the sentinel block.\n--- embedded ---\n%s\n--- library ---\n%s", embedded, want)
	}
}

func norm(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
