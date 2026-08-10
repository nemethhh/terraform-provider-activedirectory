package provider

import (
	"regexp"
	"strings"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

var guidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// identityFromImportID detects which identity space an import ID is in, so
// `terraform import activedirectory_user.jdoe jdoe` works as well as importing
// by GUID. The resource resolves it to the GUID on the way in, which is why
// state only ever holds one identity space.
func identityFromImportID(id string) adpwsh.Identity {
	id = strings.TrimSpace(id)
	switch {
	case guidPattern.MatchString(id):
		return adpwsh.ByGUID(id)
	case strings.HasPrefix(strings.ToUpper(id), "S-1-"):
		return adpwsh.BySID(id)
	case strings.Contains(id, "="):
		return adpwsh.ByDN(id)
	default:
		return adpwsh.BySAM(id)
	}
}
