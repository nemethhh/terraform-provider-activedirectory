package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
)

// envLargeCount opts the large-group suite in and sets its member count. It is
// deliberately separate from the standard AD_ACC_* variables so a plain
// `make lab-acc` does not pay for thousands of objects; the run is requested
// explicitly, e.g. AD_ACC_LARGE_COUNT=5000.
const envLargeCount = "AD_ACC_LARGE_COUNT"

// largeGroupScript provisions and tears down a large membership fixture in a
// single PowerShell pass. Building the members through the library instead
// would spawn one pwsh per user — thousands of processes — so this follows the
// sweeper's contract (the other place this repo owns PowerShell): the script is
// a constant, every value arrives as JSON on stdin, and nothing is formatted
// into script text. It exists only to exercise Group.Members past the 1500
// ranged-retrieval page boundary against a real directory.
const largeGroupScript = `
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

function Remove-Fixture($ou) {
    $found = $null
    try { $found = Get-ADOrganizationalUnit -Identity $ou @common } catch {}
    if ($found) {
        Set-ADOrganizationalUnit -Identity $ou -ProtectedFromAccidentalDeletion $false @common
        Remove-ADOrganizationalUnit -Identity $ou -Recursive -Confirm:$false @common
    }
}

try {
    switch ($p.action) {
        'provision' {
            $ou = "OU=$($p.tag),$($p.base)"
            Remove-Fixture $ou
            New-ADOrganizationalUnit -Name $p.tag -Path $p.base -ProtectedFromAccidentalDeletion:$false @common
            $g = New-ADGroup -Name "$($p.tag)-grp" -SamAccountName "$($p.tag)-grp" -GroupScope Global -GroupCategory Security -Path $ou -PassThru @common
            $dns = New-Object System.Collections.Generic.List[string]
            for ($i = 0; $i -lt $p.count; $i++) {
                $name = "$($p.tag)-m$i"
                $u = New-ADUser -Name $name -SamAccountName $name -Path $ou -Enabled $false -PassThru @common
                $dns.Add($u.DistinguishedName)
            }
            $chunk = 500
            for ($i = 0; $i -lt $dns.Count; $i += $chunk) {
                $hi = [Math]::Min($i + $chunk - 1, $dns.Count - 1)
                Add-ADGroupMember -Identity $g -Members $dns[$i..$hi] -Confirm:$false @common
            }
            $data = [ordered]@{ groupGuid = $g.ObjectGUID.ToString(); ou = $ou; count = $dns.Count }
        }
        'provision_nested' {
            $ou = "OU=$($p.tag),$($p.base)"
            Remove-Fixture $ou
            New-ADOrganizationalUnit -Name $p.tag -Path $p.base -ProtectedFromAccidentalDeletion:$false @common
            $top  = New-ADGroup -Name "$($p.tag)-top"  -SamAccountName "$($p.tag)-top"  -GroupScope Global -GroupCategory Security -Path $ou -PassThru @common
            $flat = New-ADGroup -Name "$($p.tag)-flat" -SamAccountName "$($p.tag)-flat" -GroupScope Global -GroupCategory Security -Path $ou -PassThru @common
            $buckets = [int]$p.buckets
            # Distribute count users across exactly $buckets child groups: a floor
            # share each, with the remainder spread one-per-bucket over the first
            # buckets. Every bucket is created and added to -top regardless of its
            # share, so the returned bucket count is always truthful. (count must be
            # >= buckets for every child to be non-empty, which the large-set test is.)
            $base = [Math]::Floor($p.count / $buckets)
            $rem  = $p.count - ($base * $buckets)
            $all = New-Object System.Collections.Generic.List[string]
            $made = 0
            for ($b = 0; $b -lt $buckets; $b++) {
                $child = New-ADGroup -Name "$($p.tag)-c$b" -SamAccountName "$($p.tag)-c$b" -GroupScope Global -GroupCategory Security -Path $ou -PassThru @common
                $share = $base
                if ($b -lt $rem) { $share++ }
                $dns = New-Object System.Collections.Generic.List[string]
                for ($i = 0; $i -lt $share; $i++) {
                    $name = "$($p.tag)-m$made"
                    $u = New-ADUser -Name $name -SamAccountName $name -Path $ou -Enabled $false -PassThru @common
                    $dns.Add($u.DistinguishedName); $all.Add($u.DistinguishedName); $made++
                }
                for ($i = 0; $i -lt $dns.Count; $i += 500) {
                    $hi = [Math]::Min($i + 499, $dns.Count - 1)
                    Add-ADGroupMember -Identity $child -Members $dns[$i..$hi] -Confirm:$false @common
                }
                Add-ADGroupMember -Identity $top -Members $child -Confirm:$false @common
            }
            for ($i = 0; $i -lt $all.Count; $i += 500) {
                $hi = [Math]::Min($i + 499, $all.Count - 1)
                Add-ADGroupMember -Identity $flat -Members $all[$i..$hi] -Confirm:$false @common
            }
            $data = [ordered]@{ topGuid = $top.ObjectGUID.ToString(); flatGuid = $flat.ObjectGUID.ToString(); ou = $ou; count = $made; buckets = $buckets }
        }
        'teardown' {
            Remove-Fixture $p.ou
            $data = [ordered]@{ removed = $true }
        }
        default { throw "unknown action: $($p.action)" }
    }
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

type largeGroupResult struct {
	GroupGUID string `json:"groupGuid"`
	TopGUID   string `json:"topGuid"`
	FlatGUID  string `json:"flatGuid"`
	OU        string `json:"ou"`
	Count     int    `json:"count"`
	Buckets   int    `json:"buckets"`
	Removed   bool   `json:"removed"`
}

// runLargeGroup runs one action of largeGroupScript, threading the same server
// and credential the sweeper uses so it obeys the double hop over SSH.
func runLargeGroup(ctx context.Context, tr *adlocal.Transport, payload map[string]any) (largeGroupResult, error) {
	if v := os.Getenv(envServer); v != "" {
		payload["server"] = v
	}
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		payload["credential"] = map[string]any{"username": u, "password": p}
	}
	var out largeGroupResult
	body, err := json.Marshal(payload)
	if err != nil {
		return out, fmt.Errorf("large-group: encode payload: %w", err)
	}
	res, err := tr.Run(ctx, sweepEncodeCommand(largeGroupScript), body)
	if err != nil {
		return out, fmt.Errorf("large-group: run: %w", err)
	}
	if res.ExitCode != 0 {
		return out, fmt.Errorf("large-group: pwsh exited %d: %s", res.ExitCode, res.Stderr)
	}
	data, err := sweepEnvelopeData(res.Stdout)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("large-group: decode result: %w", err)
	}
	return out, nil
}

// TestAccGroupMembershipLargeSet proves Group.Members reads a group whose
// membership exceeds the 1500-entry ranged-retrieval page — the case the fake
// cannot exercise and the reason v0.3.1 stopped hand-ranging the read. It
// provisions AD_ACC_LARGE_COUNT members in one pass, reads them back through the
// library, and tears the fixture down. Opt-in: skipped unless AD_ACC_LARGE_COUNT
// is set, so it does not weigh down the ordinary acceptance run.
func TestAccGroupMembershipLargeSet(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test; set TF_ACC=1")
	}
	countStr := os.Getenv(envLargeCount)
	if countStr == "" {
		t.Skipf("%s is not set; skipping the large-group suite", envLargeCount)
	}
	count, err := strconv.Atoi(countStr)
	if err != nil || count <= 0 {
		t.Fatalf("%s=%q must be a positive integer", envLargeCount, countStr)
	}
	container := os.Getenv(envContainer)
	if container == "" {
		t.Fatalf("%s must be set", envContainer)
	}

	ctx := context.Background()
	tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath), Timeout: 15 * time.Minute})
	if err != nil {
		t.Fatalf("start PowerShell: %v", err)
	}
	defer func() { _ = tr.Close() }()

	tag := accNamePrefix + "large"
	prov, err := runLargeGroup(ctx, tr, map[string]any{
		"action": "provision", "base": container, "tag": tag, "count": count,
	})
	if err != nil {
		t.Fatalf("provision %d members: %v", count, err)
	}
	t.Cleanup(func() {
		if _, err := runLargeGroup(context.Background(), tr, map[string]any{
			"action": "teardown", "ou": prov.OU,
		}); err != nil {
			t.Errorf("teardown %s: %v", prov.OU, err)
		}
	})

	cfg := adpwsh.Config{Transport: tr, Server: os.Getenv(envServer)}
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		cfg.Credential = &adpwsh.Credential{Username: u, Password: adpwsh.NewSecret(p)}
	}
	client, err := adpwsh.New(ctx, cfg)
	if err != nil {
		t.Fatalf("configure client: %v", err)
	}

	members, err := client.Group.Members(ctx, adpwsh.ByGUID(prov.GroupGUID))
	if err != nil {
		t.Fatalf("Group.Members: %v", err)
	}
	if len(members) != count {
		t.Fatalf("Group.Members returned %d, want %d — ranged retrieval past the 1500 page boundary",
			len(members), count)
	}
	t.Logf("read back all %d members of a real group (%d ranged pages)", count, (count+1499)/1500)
}
