package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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

// samIllegalCharsValidators is the character-set and no-trailing-period/space
// rule every sAMAccountName validator set shares (user, group, gMSA) — only
// the length ceiling differs per object class, so this is the one place the
// illegal-character check itself is written.
func samIllegalCharsValidators() []validator.String {
	return []validator.String{
		stringvalidator.RegexMatches(samAccountNameCharset,
			`must not contain any of " [ ] : ; | = + * ? < > / \ ,`),
		stringvalidator.RegexMatches(samAccountNameNoTrailer,
			"must not end with a period or space"),
	}
}

// samAccountNameValidatorsMax builds the shared charset/no-trailer rules
// against a caller-supplied length ceiling, so user and group can each pin
// their own maximum without duplicating the character-set validation.
func samAccountNameValidatorsMax(maxLen int) []validator.String {
	return append([]validator.String{
		stringvalidator.LengthBetween(1, maxLen),
	}, samIllegalCharsValidators()...)
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

// gmsaSamAccountNameMaxLen is the gMSA sam_account_name ceiling: a gMSA is a
// computer-like account, and Active Directory appends a trailing "$" to the
// down-level logon name it stores, so the 15-character NetBIOS computer-name
// limit applies to the name as configured here (before that suffix).
const gmsaSamAccountNameMaxLen = 15

// gmsaSamAccountNameValidators is the GMSA sam_account_name validator set:
// the 15-character computer-name ceiling plus the same illegal-character/
// no-trailer rule every other sAMAccountName validator enforces.
func gmsaSamAccountNameValidators() []validator.String {
	return append([]validator.String{
		stringvalidator.LengthAtMost(gmsaSamAccountNameMaxLen),
	}, samIllegalCharsValidators()...)
}

// warnLongSamValidator backs warnLongSam: a length check that only ever
// warns. Unlike every other sAMAccountName ceiling in this file, AD does
// *not* enforce the 15-character NetBIOS limit for computer accounts
// (lab-confirmed) — so the provider must not out-strict the directory by
// rejecting a value AD itself accepts. A trailing "$" (the down-level logon
// name suffix AD stores) is stripped before measuring length, since the
// caller configures the name without it.
type warnLongSamValidator struct{ maxLen int }

func (v warnLongSamValidator) Description(context.Context) string {
	return fmt.Sprintf("warns when the sAMAccountName exceeds %d characters", v.maxLen)
}

func (v warnLongSamValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v warnLongSamValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := strings.TrimSuffix(req.ConfigValue.ValueString(), "$")
	if len(s) > v.maxLen {
		resp.Diagnostics.AddAttributeWarning(req.Path,
			"Computer name longer than NetBIOS limit",
			fmt.Sprintf("sAMAccountName %q is %d characters. AD allows this, but computers with names longer than %d characters can hit NetBIOS/domain-join problems.", s, len(s), v.maxLen))
	}
}

// warnLongSam returns a warning-only length validator: it never blocks a
// plan, it only surfaces a diagnostic so the practitioner can make an
// informed choice. This is the first warning-only validator in the repo —
// every other sAMAccountName ceiling here is a hard error because the lab
// proved AD itself rejects (or silently alters) the value.
func warnLongSam(maxLen int) validator.String { return warnLongSamValidator{maxLen: maxLen} }

// computerSamAccountNameValidators is the COMPUTER sam_account_name
// validator set: the same illegal-character/no-trailer rule every other
// sAMAccountName validator enforces, plus a *warning* (not an error) past
// the 15-character NetBIOS computer-name limit — the lab proved AD does not
// enforce that ceiling for computer accounts the way it does for gMSAs.
func computerSamAccountNameValidators() []validator.String {
	return append(samIllegalCharsValidators(), warnLongSam(15))
}

// kerberosEncryptionTypeValues is the set of values Active Directory accepts
// for a gMSA's KerberosEncryptionType, in the order the schema's
// kerberos_encryption_type set validator (Task 8) enumerates them.
func kerberosEncryptionTypeValues() []string {
	return []string{"None", "DES", "RC4", "AES128", "AES256"}
}

// cnMaxLen is the schema ceiling on cn; a longer name is truncated by the
// directory, so the provider refuses it at plan time.
const cnMaxLen = 64

func cnLengthValidators() []validator.String {
	return []validator.String{stringvalidator.LengthAtMost(cnMaxLen)}
}
