package provider

import (
	"sort"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// compileFilter turns the two filter inputs of a plural search into one LDAP
// filter. filter_by values are escaped through the library and ANDed in sorted
// key order (stable output ⇒ stable plans); a raw ldap_filter is passed through
// verbatim. All syntax is built by the library's builder — never by hand.
func compileFilter(filterBy map[string]string, ldap string) string {
	terms := make([]string, 0, len(filterBy)+1)
	keys := make([]string, 0, len(filterBy))
	for k := range filterBy {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		terms = append(terms, adpwsh.Equal(k, filterBy[k]))
	}
	if ldap != "" {
		terms = append(terms, ldap)
	}
	return adpwsh.And(terms...)
}
