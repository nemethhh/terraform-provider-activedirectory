package provider

import (
	"strings"
	"testing"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

func TestErrorDiagnosticsAlreadyExistsEmitsAnImportBlock(t *testing.T) {
	err := &adpwsh.Error{
		Kind: adpwsh.KindAlreadyExists, Op: "User.Create",
		Target:        "CN=jdoe,OU=Staff,DC=corp,DC=local",
		ServerMessage: "The specified account already exists",
	}
	diags := errorDiagnostics("User.Create", "activedirectory_user", err)
	if !diags.HasError() {
		t.Fatal("expected an error diagnostic")
	}
	detail := diags.Errors()[0].Detail()
	for _, want := range []string{
		"import {",
		"to = activedirectory_user.",
		`id = "CN=jdoe,OU=Staff,DC=corp,DC=local"`,
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail is missing %q:\n%s", want, detail)
		}
	}
}

// A tombstone is a different problem with a different fix, and saying
// "already exists" alone sends the operator hunting for an object they cannot
// see.
func TestErrorDiagnosticsTombstonePointsAtRestore(t *testing.T) {
	err := &adpwsh.Error{
		Kind: adpwsh.KindAlreadyExists, Op: "User.Create", Tombstoned: true,
		Target: `CN=jdoe\0ADEL:…,CN=Deleted Objects,DC=corp,DC=local`,
	}
	detail := errorDiagnostics("User.Create", "activedirectory_user", err).Errors()[0].Detail()
	if !strings.Contains(detail, "Restore-ADObject") || !strings.Contains(detail, "deleted object") {
		t.Errorf("detail should name the deleted object and Restore-ADObject:\n%s", detail)
	}
}

// A replication timeout is a warning about a completed write, not a failure.
// Rendering it as an error with no state is what orphans objects.
func TestErrorDiagnosticsReplicationTimeoutIsRecognisable(t *testing.T) {
	err := &adpwsh.Error{Kind: adpwsh.KindReplication, Op: "OU.Create"}
	if !isReplicationTimeout(err) {
		t.Fatal("isReplicationTimeout must recognise KindReplication")
	}
	detail := errorDiagnostics("OU.Create", "activedirectory_ou", err).Errors()[0].Detail()
	if !strings.Contains(detail, "state has been saved") {
		t.Errorf("the operator must be told the object exists and state was saved:\n%s", detail)
	}
}

func TestErrorDiagnosticsCarriesTheServersOwnWords(t *testing.T) {
	err := &adpwsh.Error{
		Kind: adpwsh.KindDenied, Op: "OU.Delete",
		Identity: "guid:9f2c", Target: "OU=Staff,DC=corp,DC=local",
		ExceptionType: "Microsoft.ActiveDirectory.Management.ADException",
		Code:          0x2098,
		ServerMessage: "Insufficient access rights to perform the operation",
	}
	d := errorDiagnostics("OU.Delete", "activedirectory_ou", err).Errors()[0]
	if !strings.Contains(d.Detail(), "Insufficient access rights") {
		t.Errorf("detail lost the server message:\n%s", d.Detail())
	}
	if !strings.Contains(d.Detail(), "OU=Staff,DC=corp,DC=local") {
		t.Errorf("detail must name the target:\n%s", d.Detail())
	}
	if !strings.Contains(d.Summary(), "denied") {
		t.Errorf("summary = %q", d.Summary())
	}
}

// A password failure must never echo the value, and there is nothing in the
// error to echo — but assert it, because #197 was exactly this.
func TestErrorDiagnosticsNeverEchoesASecret(t *testing.T) {
	err := &adpwsh.Error{
		Kind: adpwsh.KindPassword, Op: "User.SetPassword",
		ServerMessage: "The password does not meet the complexity requirements",
	}
	detail := errorDiagnostics("User.SetPassword", "activedirectory_user", err).Errors()[0].Detail()
	if strings.Contains(strings.ToLower(detail), "hunter2") {
		t.Errorf("detail leaked a password:\n%s", detail)
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(&adpwsh.Error{Kind: adpwsh.KindNotFound}) {
		t.Error("KindNotFound must be recognised")
	}
	if isNotFound(&adpwsh.Error{Kind: adpwsh.KindDenied}) {
		t.Error("only KindNotFound removes a resource from state")
	}
	if isNotFound(nil) {
		t.Error("nil is not a not-found")
	}
}
