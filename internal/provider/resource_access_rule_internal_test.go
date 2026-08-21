package provider

import (
	"context"
	"testing"

	adpwsh "github.com/nemethhh/go-adpwsh"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

// TestInferObjectTypeRefKind exercises ruling P2 directly: the fake's
// handleSchemaResolve ignores the requested Kind entirely (it looks names up
// by name only), so the lifecycle test against the fake cannot distinguish
// RefClass from RefAttribute from RefExtendedRight. Only a direct unit test on
// the pure function can.
func TestInferObjectTypeRefKind(t *testing.T) {
	cases := []struct {
		name   string
		rights []adpwsh.Right
		want   adpwsh.SchemaRefKind
	}{
		{"CreateChild alone", []adpwsh.Right{"CreateChild"}, adpwsh.RefClass},
		{"DeleteChild alone", []adpwsh.Right{"DeleteChild"}, adpwsh.RefClass},
		{"ExtendedRight alone", []adpwsh.Right{"ExtendedRight"}, adpwsh.RefExtendedRight},
		{"ReadProperty alone", []adpwsh.Right{"ReadProperty"}, adpwsh.RefAttribute},
		{"WriteProperty alone", []adpwsh.Right{"WriteProperty"}, adpwsh.RefAttribute},
		{"GenericAll alone", []adpwsh.Right{"GenericAll"}, adpwsh.RefAttribute},
		{"CreateChild wins over ReadProperty", []adpwsh.Right{"CreateChild", "ReadProperty"}, adpwsh.RefClass},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inferObjectTypeRefKind(tc.rights); got != tc.want {
				t.Errorf("inferObjectTypeRefKind(%v) = %v, want %v", tc.rights, got, tc.want)
			}
		})
	}
}

// newTestClient builds a real *adpwsh.Client over an arbitrary Transport, for
// tests that need to drive a client method directly rather than through a
// Terraform apply.
func newTestClient(t *testing.T, tr adpwsh.Transport) *adpwsh.Client {
	t.Helper()
	client, err := adpwsh.New(context.Background(), adpwsh.Config{Transport: tr})
	if err != nil {
		t.Fatalf("adpwsh.New: %v", err)
	}
	return client
}

// TestResolveTrusteeSID exercises ruling P3 directly. The lifecycle test
// against the fake only ever configures a group trustee, so it cannot prove
// the SID-shaped short-circuit, the user fallback, or the both-miss error.
func TestResolveTrusteeSID(t *testing.T) {
	t.Run("SID-shaped input passes through verbatim, no client needed", func(t *testing.T) {
		r := &accessRuleResource{} // client is nil: resolveTrusteeSID must not touch it here
		sid, diags := r.resolveTrusteeSID(context.Background(), "S-1-5-21-1-2-3-1000")
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if sid != "S-1-5-21-1-2-3-1000" {
			t.Errorf("got %q, want the input echoed back verbatim", sid)
		}
	})

	dir := fake.NewDirectory()
	groupGUID := dir.Seed("group", "helpdesk", "DC=corp,DC=local", map[string]any{
		"samAccountName": "helpdesk", "sid": "S-1-5-21-1-2-3-2000",
	})
	userGUID := dir.Seed("user", "jdoe", "DC=corp,DC=local", map[string]any{
		"samAccountName": "jdoe", "sid": "S-1-5-21-1-2-3-2001",
	})

	t.Run("a group identity resolves to the group's SID", func(t *testing.T) {
		r := &accessRuleResource{client: newTestClient(t, dir.Transport())}
		sid, diags := r.resolveTrusteeSID(context.Background(), groupGUID)
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if sid != "S-1-5-21-1-2-3-2000" {
			t.Errorf("got %q, want the group's SID", sid)
		}
	})

	t.Run("a user identity falls through Group.Get's error to User.Get", func(t *testing.T) {
		// fake.Directory's generic read handler (ou_read/group_read/user_read)
		// does not filter by class, so Group.Get(userGUID) against the plain
		// directory would succeed trivially and this case would prove
		// nothing. Wrap the transport so group_read always fails with a
		// non-not-found error, and everything else (including user_read)
		// still goes to the real directory: this is what actually forces
		// resolveTrusteeSID down the fallback branch, and proves the
		// fallback is gated on "any error", not just isNotFound (ruling P3).
		// UnauthorizedAccessException classifies as KindDenied — distinctly
		// not KindNotFound, and (unlike e.g. ADServerDownException/
		// KindTransient) not retried, so this stays fast.
		handler := func(c fake.Call) fake.Response {
			if c.Op == "group_read" {
				return fake.Fail("UnauthorizedAccessException",
					"simulated: access denied", 0)
			}
			return dir.Handle(c)
		}
		r := &accessRuleResource{client: newTestClient(t, fake.New(handler))}
		sid, diags := r.resolveTrusteeSID(context.Background(), userGUID)
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if sid != "S-1-5-21-1-2-3-2001" {
			t.Errorf("got %q, want the user's SID", sid)
		}
	})

	t.Run("an unknown identity errors on both", func(t *testing.T) {
		r := &accessRuleResource{client: newTestClient(t, dir.Transport())}
		_, diags := r.resolveTrusteeSID(context.Background(), "no-such-trustee")
		if !diags.HasError() {
			t.Fatal("expected an attribute error, got none")
		}
	})
}
