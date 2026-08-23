package provider

import (
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Active Directory enforces these on a down-level logon name (sAMAccountName):
// a length ceiling and a character set. A value that violates them is rejected
// or silently altered by the directory, so the provider refuses it at plan time
// rather than let a truncation cascade into a perpetual diff. The exact ceiling
// and set are confirmed against the lab (LAB.md) before release.
//
// The length ceiling is not one number: a lab probe proved a real domain
// rejects a USER sam past 20 characters (the down-level logon name limit) but
// accepts and stores a 25-character GROUP sam. samAccountNameMaxLen is the
// user ceiling; groupSamAccountNameMaxLen is the schema ceiling on
// sAMAccountName (rangeUpper) that applies to groups instead.
const (
	samAccountNameMaxLen      = 20
	groupSamAccountNameMaxLen = 256
)

// The characters a sAMAccountName may not contain, as a negated character class.
// RE2 has no lookarounds, so the trailing dot/space rule is a second pattern.
// Both are confirmed against the lab for user and group alike, so they are
// shared regardless of which length ceiling applies.
var (
	samAccountNameCharset   = regexp.MustCompile(`^[^"\[\]:;|=+*?<>/\\,]+$`)
	samAccountNameNoTrailer = regexp.MustCompile(`[^. ]$`)
)

// samAccountNameValidatorsMax builds the shared charset/no-trailer rules
// against a caller-supplied length ceiling, so user and group can each pin
// their own maximum without duplicating the character-set validation.
func samAccountNameValidatorsMax(maxLen int) []validator.String {
	return []validator.String{
		stringvalidator.LengthBetween(1, maxLen),
		stringvalidator.RegexMatches(samAccountNameCharset,
			`must not contain any of " [ ] : ; | = + * ? < > / \ ,`),
		stringvalidator.RegexMatches(samAccountNameNoTrailer,
			"must not end with a period or space"),
	}
}

// samAccountNameValidators is the USER sam_account_name validator set: the
// lab proved Active Directory rejects a down-level logon name past 20
// characters.
func samAccountNameValidators() []validator.String {
	return samAccountNameValidatorsMax(samAccountNameMaxLen)
}

// groupSamAccountNameValidators is the GROUP sam_account_name validator set:
// the lab proved Active Directory accepts a group sam well past the user's
// 20-char limit, so groups are only bound by the sAMAccountName schema
// ceiling.
func groupSamAccountNameValidators() []validator.String {
	return samAccountNameValidatorsMax(groupSamAccountNameMaxLen)
}

// cnMaxLen is the schema ceiling on cn; a longer name is truncated by the
// directory, so the provider refuses it at plan time.
const cnMaxLen = 64

func cnLengthValidators() []validator.String {
	return []validator.String{stringvalidator.LengthAtMost(cnMaxLen)}
}
