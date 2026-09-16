package provider

import (
	"errors"
	"strings"
	"testing"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// The advice that follows a failed adpwsh.New is dialect-specific. Telling a
// psopenad user to install RSAT and open 9389 is not merely unhelpful — neither
// is involved in an LDAP session, so it sends them to look in the wrong place.
func TestClientErrDetailIsDialectAware(t *testing.T) {
	err := errors.New("boom")

	adws := clientErrDetail(adpwsh.DialectADWS, err)
	if !strings.Contains(adws, "RSAT-AD-PowerShell") || !strings.Contains(adws, "9389") {
		t.Errorf("adws detail lost its advice: %s", adws)
	}

	open := clientErrDetail(adpwsh.DialectPSOpenAD, err)
	if !strings.Contains(open, "PSOpenAD") || !strings.Contains(open, "LDAP") {
		t.Errorf("psopenad detail does not name the module or the protocol: %s", open)
	}
	if strings.Contains(open, "RSAT-AD-PowerShell") || strings.Contains(open, "9389") {
		t.Errorf("psopenad detail carries adws advice: %s", open)
	}

	for _, s := range []string{adws, open} {
		if !strings.Contains(s, "boom") {
			t.Errorf("detail dropped the underlying error: %s", s)
		}
	}
}
