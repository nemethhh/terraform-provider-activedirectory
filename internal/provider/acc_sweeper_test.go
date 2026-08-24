package provider_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
)

// TestMain adds the -sweep flag to this package's test binary. Without -sweep it
// simply runs the tests, so ordinary `go test ./...` is unaffected.
func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func init() {
	resource.AddTestSweepers("activedirectory_tfacc", &resource.Sweeper{
		Name: "activedirectory_tfacc",
		F:    sweepTestObjects,
	})
	resource.AddTestSweepers("activedirectory_e2e", &resource.Sweeper{
		Name: "activedirectory_e2e",
		F:    sweepE2EObjects,
	})
}

// sweepScript is the one piece of PowerShell this repository owns. The library's
// op set is closed and exposes no directory search, and a sweeper that cannot
// enumerate is not a sweeper. It obeys the library's own rules: the script is a
// constant, every value arrives as JSON on stdin, and no value is ever formatted
// into script text.
const sweepScript = `
$ErrorActionPreference = 'Stop'
$ProgressPreference    = 'SilentlyContinue'
Import-Module ActiveDirectory -ErrorAction Stop
$p = [Console]::In.ReadToEnd() | ConvertFrom-Json -AsHashtable

$common = @{}
if ($p.server) { $common['Server'] = $p.server }
if ($p.credential) {
    $secpw = ConvertTo-SecureString $p.credential.password -AsPlainText -Force
    $common['Credential'] = [System.Management.Automation.PSCredential]::new($p.credential.username, $secpw)
}

try {
    $found = @(Get-ADObject -SearchBase $p.searchBase -SearchScope Subtree -LDAPFilter $p.filter -Properties objectClass @common)
    $data = [ordered]@{ objects = @($found | ForEach-Object { [ordered]@{
        objectGUID        = $_.ObjectGUID.ToString()
        distinguishedName = $_.DistinguishedName
        objectClass       = $_.ObjectClass
    } }) }
    $out = @{ ok = $true; data = $data }
} catch {
    $out = @{ ok = $false; error = @{
        type    = $_.Exception.GetType().FullName
        message = $_.Exception.Message
    } }
}
Write-Output '<<<TFAD:BEGIN>>>'
Write-Output ($out | ConvertTo-Json -Depth 6 -Compress)
Write-Output '<<<TFAD:END>>>'
`

// sweepServiceAccountScript removes gMSAs directly with Get-ADServiceAccount /
// Remove-ADServiceAccount, the idiomatic pair for this AD object type, rather
// than folding it into sweepScript's generic Get-ADObject search: the library's
// ServiceAccount client is get-by-identity only (no search of its own), which is
// the same gap that gives the OU/group/user sweep its own PowerShell discovery.
// Same contract as sweepScript: the script is a constant, every value arrives as
// JSON on stdin, and no value is ever formatted into script text.
const sweepServiceAccountScript = `
$ErrorActionPreference = 'Stop'
$ProgressPreference    = 'SilentlyContinue'
Import-Module ActiveDirectory -ErrorAction Stop
$p = [Console]::In.ReadToEnd() | ConvertFrom-Json -AsHashtable

$common = @{}
if ($p.server) { $common['Server'] = $p.server }
if ($p.credential) {
    $secpw = ConvertTo-SecureString $p.credential.password -AsPlainText -Force
    $common['Credential'] = [System.Management.Automation.PSCredential]::new($p.credential.username, $secpw)
}

$removed = New-Object System.Collections.Generic.List[object]
$failed  = New-Object System.Collections.Generic.List[object]

try {
    $found = @(Get-ADServiceAccount -SearchBase $p.searchBase -SearchScope Subtree -LDAPFilter $p.filter @common)
    foreach ($a in $found) {
        if (-not $a.DistinguishedName.ToUpper().EndsWith($p.searchBase.ToUpper())) {
            continue # belt and braces: outside the subtree the search asked for
        }
        try {
            Remove-ADServiceAccount -Identity $a.ObjectGUID -Confirm:$false @common
            $removed.Add([ordered]@{
                objectGUID        = $a.ObjectGUID.ToString()
                distinguishedName = $a.DistinguishedName
            })
        } catch {
            if ($_.Exception.GetType().FullName -like '*IdentityNotFoundException*') {
                continue # already gone: a previous sweep or a cascading delete took it
            }
            $failed.Add([ordered]@{
                distinguishedName = $a.DistinguishedName
                message           = $_.Exception.Message
            })
        }
    }
    $data = [ordered]@{ removed = @($removed); failed = @($failed) }
    $out = @{ ok = $true; data = $data }
} catch {
    $out = @{ ok = $false; error = @{
        type    = $_.Exception.GetType().FullName
        message = $_.Exception.Message
    } }
}
Write-Output '<<<TFAD:BEGIN>>>'
Write-Output ($out | ConvertTo-Json -Depth 6 -Compress)
Write-Output '<<<TFAD:END>>>'
`

const (
	sweepSentinelBegin = "<<<TFAD:BEGIN>>>"
	sweepSentinelEnd   = "<<<TFAD:END>>>"
)

type sweepObject struct {
	GUID  string `json:"objectGUID"`
	DN    string `json:"distinguishedName"`
	Class string `json:"objectClass"`
}

// sweepTestObjects sweeps beneath AD_ACC_CONTAINER (the base acceptance subtree).
func sweepTestObjects(string) error {
	container := os.Getenv(envContainer)
	if container == "" {
		return fmt.Errorf("%s must be set to sweep", envContainer)
	}
	return sweepBeneath(container)
}

// sweepE2EObjects sweeps beneath AD_E2E_CONTAINER. Unset is a no-op, not an
// error, so a plain `-sweep=domain` in an environment that only provisioned the
// base fixtures does not fail — the same opt-in posture as the e2e suites.
func sweepE2EObjects(string) error {
	container := os.Getenv(envE2EContainer)
	if container == "" {
		log.Printf("[INFO] sweep: %s is not set; skipping the e2e subtree", envE2EContainer)
		return nil
	}
	return sweepBeneath(container)
}

// sweepBeneath deletes every object beneath container whose name begins with
// tfacc-, deepest first, lifting ProtectedFromAccidentalDeletion as it goes.
//
// It never deletes anything else. container itself is treated as pre-existing —
// the suite neither creates nor destroys it — and the prefix match is what keeps
// a sweep from touching an object a human placed in the subtree by hand.
func sweepBeneath(container string) error {
	ctx := context.Background()

	// A sweep can be large and follows a crash, so it gets a generous ceiling
	// rather than the per-operation default.
	tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath), Timeout: 5 * time.Minute})
	if err != nil {
		return fmt.Errorf("sweep: cannot start PowerShell: %w", err)
	}
	defer func() { _ = tr.Close() }()

	// Service accounts first: like users and groups, a gMSA lives beneath the
	// OUs the pass below deletes, and it is not one of the classes that pass's
	// switch handles (a gMSA's ObjectClass is msDS-GroupManagedServiceAccount,
	// not one Get-ADObject would let this repo dispatch on generically).
	if err := sweepServiceAccounts(ctx, tr, container); err != nil {
		return err
	}

	objects, err := sweepDiscover(ctx, tr, container)
	if err != nil {
		return err
	}
	if len(objects) == 0 {
		log.Printf("[INFO] sweep: nothing beneath %s is named %s*", container, accNamePrefix)
		return nil
	}

	// Deepest first: a parent OU cannot be deleted while it still has children,
	// and recursive deletion is deliberately unreachable from the library's API.
	sort.SliceStable(objects, func(i, j int) bool {
		return dnDepth(objects[i].DN) > dnDepth(objects[j].DN)
	})

	cfg := adpwsh.Config{Transport: tr, Server: os.Getenv(envServer)}
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		cfg.Credential = &adpwsh.Credential{Username: u, Password: adpwsh.NewSecret(p)}
	}
	client, err := adpwsh.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("sweep: cannot configure the Active Directory client: %w", err)
	}

	var failures []string
	for _, o := range objects {
		// Belt and braces. The search already restricted itself to the subtree
		// and the prefix; this refuses anything that is somehow not both, and
		// refuses the container itself even if a human named it tfacc-.
		if !strings.HasSuffix(strings.ToUpper(o.DN), strings.ToUpper(container)) {
			log.Printf("[WARN] sweep: leaving %s alone; it is not beneath %s", o.DN, container)
			continue
		}
		if strings.EqualFold(o.DN, container) {
			continue
		}

		var derr error
		switch strings.ToLower(o.Class) {
		case "organizationalunit":
			// Unprotect: the destroy path AD's own default requires.
			derr = client.OU.Delete(ctx, adpwsh.ByGUID(o.GUID), adpwsh.DeleteOptions{Unprotect: true})
		case "group":
			derr = client.Group.Delete(ctx, adpwsh.ByGUID(o.GUID))
		case "user":
			derr = client.User.Delete(ctx, adpwsh.ByGUID(o.GUID))
		default:
			log.Printf("[WARN] sweep: leaving %s alone; class %q is not one this suite creates",
				o.DN, o.Class)
			continue
		}
		// Already gone is success: a deeper delete may have taken it, or a
		// previous sweep did.
		if derr != nil && !errors.Is(derr, adpwsh.ErrNotFound) {
			failures = append(failures, fmt.Sprintf("%s: %s", o.DN, derr))
			continue
		}
		log.Printf("[INFO] sweep: deleted %s", o.DN)
	}
	if len(failures) > 0 {
		return fmt.Errorf("sweep left %d object(s) behind:\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
	return nil
}

// sweepServiceAccounts removes gMSAs beneath container whose name begins with
// accNamePrefix. Unlike sweepDiscover+sweepBeneath's per-object loop, discovery
// and deletion happen in one round trip inside sweepServiceAccountScript, since
// Get-ADServiceAccount/Remove-ADServiceAccount is the pair this AD object type
// wants; a not-found on removal (already gone: a previous sweep or a cascading
// delete took it) is handled inside the script and never reaches Go as a failure.
func sweepServiceAccounts(ctx context.Context, tr *adlocal.Transport, container string) error {
	payload := map[string]any{
		"searchBase": container,
		// Matches the same tfacc- prefix as the generic sweep; a gMSA's name is
		// its cn, exactly like a group's or a user's.
		"filter": "(name=" + accNamePrefix + "*)",
	}
	if v := os.Getenv(envServer); v != "" {
		payload["server"] = v
	}
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		payload["credential"] = map[string]any{"username": u, "password": p}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sweep: cannot encode the service account search payload: %w", err)
	}

	res, err := tr.Run(ctx, sweepEncodeCommand(sweepServiceAccountScript), body)
	if err != nil {
		return fmt.Errorf("sweep: cannot run the service account cleanup: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("sweep: pwsh exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	data, err := sweepEnvelopeData(res.Stdout)
	if err != nil {
		return err
	}

	var out struct {
		Removed []sweepObject `json:"removed"`
		Failed  []struct {
			DN      string `json:"distinguishedName"`
			Message string `json:"message"`
		} `json:"failed"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("sweep: cannot decode the service account cleanup result: %w", err)
	}

	if len(out.Removed) == 0 && len(out.Failed) == 0 {
		log.Printf("[INFO] sweep: nothing beneath %s is a service account named %s*", container, accNamePrefix)
		return nil
	}
	for _, o := range out.Removed {
		log.Printf("[INFO] sweep: deleted %s", o.DN)
	}
	if len(out.Failed) > 0 {
		msgs := make([]string, len(out.Failed))
		for i, f := range out.Failed {
			msgs[i] = fmt.Sprintf("%s: %s", f.DN, f.Message)
		}
		return fmt.Errorf("sweep left %d service account(s) behind:\n  %s",
			len(out.Failed), strings.Join(msgs, "\n  "))
	}
	return nil
}

// sweepDiscover runs the search and returns what it found.
func sweepDiscover(ctx context.Context, tr *adlocal.Transport, container string) ([]sweepObject, error) {
	payload := map[string]any{
		"searchBase": container,
		// name matches every class the suite creates: an OU's name is its ou
		// attribute, a group's and a user's is its cn, and Active Directory
		// surfaces all three as name.
		"filter": "(name=" + accNamePrefix + "*)",
	}
	if v := os.Getenv(envServer); v != "" {
		payload["server"] = v
	}
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		payload["credential"] = map[string]any{"username": u, "password": p}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sweep: cannot encode the search payload: %w", err)
	}

	res, err := tr.Run(ctx, sweepEncodeCommand(sweepScript), body)
	if err != nil {
		return nil, fmt.Errorf("sweep: cannot run the search: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("sweep: pwsh exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	data, err := sweepEnvelopeData(res.Stdout)
	if err != nil {
		return nil, err
	}
	var out struct {
		Objects []sweepObject `json:"objects"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("sweep: cannot decode the search result: %w", err)
	}
	return out.Objects, nil
}

// sweepEncodeCommand renders script the way pwsh -EncodedCommand expects. The
// library's encoder is internal, and seven lines here is better than widening
// its public surface for a test-only sweeper.
func sweepEncodeCommand(script string) string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[i*2:], u)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// sweepEnvelopeData extracts the JSON the script wrote between its sentinels and
// returns the data half. A missing envelope means pwsh failed before the script
// ran, which is a transport problem and is reported as such.
func sweepEnvelopeData(stdout string) (json.RawMessage, error) {
	begin := strings.Index(stdout, sweepSentinelBegin)
	end := strings.Index(stdout, sweepSentinelEnd)
	if begin < 0 || end < begin {
		return nil, fmt.Errorf("sweep: no envelope in the search output: %s", strings.TrimSpace(stdout))
	}
	body := strings.TrimSpace(stdout[begin+len(sweepSentinelBegin) : end])

	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return nil, fmt.Errorf("sweep: cannot decode the envelope %q: %w", body, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("sweep: the search failed: %s (%s)", env.Error.Message, env.Error.Type)
		}
		return nil, errors.New("sweep: the search failed with no error detail")
	}
	return env.Data, nil
}

// dnDepth counts a distinguished name's components. An RDN value may contain an
// escaped comma — `OU=Sales\, EMEA` is one component, not two — so counting
// every comma would sort the wrong object first and the sweep would try to
// delete a parent before its child.
func dnDepth(dn string) int {
	depth := 1
	for i := 0; i < len(dn); i++ {
		switch dn[i] {
		case '\\':
			i++ // skip the escaped character, whatever it is
		case ',':
			depth++
		}
	}
	return depth
}

// The escaped comma is the case this counter exists for: get it wrong and the
// sweep deletes a parent before its child and fails on the non-leaf.
func TestDNDepthCountsUnescapedCommasOnly(t *testing.T) {
	tests := []struct {
		dn   string
		want int
	}{
		{"DC=local", 1},
		{"DC=corp,DC=local", 2},
		{"OU=tfacc,DC=corp,DC=local", 3},
		{`OU=tfacc-hostile\, EMEA,DC=corp,DC=local`, 3},
		{`CN=g,OU=tfacc-hostile\, EMEA,DC=corp,DC=local`, 4},
	}
	for _, tt := range tests {
		if got := dnDepth(tt.dn); got != tt.want {
			t.Errorf("dnDepth(%q) = %d, want %d", tt.dn, got, tt.want)
		}
	}
}

// The sentinel parser is the other testable half: a pwsh that died before the
// script ran produces no envelope, and that must be a named failure rather than
// a nil-pointer panic.
func TestSweepEnvelopeData(t *testing.T) {
	ok := sweepSentinelBegin + "\r\n" + `{"ok":true,"data":{"objects":[]}}` + "\r\n" + sweepSentinelEnd
	data, err := sweepEnvelopeData(ok)
	if err != nil {
		t.Fatalf("sweepEnvelopeData: %v", err)
	}
	if string(data) != `{"objects":[]}` {
		t.Errorf("data = %s", data)
	}

	if _, err := sweepEnvelopeData("Import-Module : module not loaded"); err == nil {
		t.Error("output with no envelope must be an error")
	}
	failed := sweepSentinelBegin + "\n" +
		`{"ok":false,"error":{"type":"ADException","message":"nope"}}` + "\n" + sweepSentinelEnd
	if _, err := sweepEnvelopeData(failed); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("a failed search must surface the message: %v", err)
	}
}

// The e2e sweeper must be a no-op — not an error — when its container is unset,
// so a base-only sweep is unaffected.
func TestSweepE2ENoopWhenUnset(t *testing.T) {
	t.Setenv(envE2EContainer, "")
	if err := sweepE2EObjects("domain"); err != nil {
		t.Fatalf("sweepE2EObjects with %s unset must be a no-op, got: %v", envE2EContainer, err)
	}
}
