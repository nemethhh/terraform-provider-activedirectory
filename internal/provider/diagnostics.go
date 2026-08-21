package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// isNotFound reports whether an error means the object is gone, which during
// Read means the resource leaves state rather than the apply failing.
func isNotFound(err error) bool { return err != nil && errors.Is(err, adpwsh.ErrNotFound) }

// isReplicationTimeout reports the one condition where a write succeeded and
// an error must still be surfaced.
func isReplicationTimeout(err error) bool {
	return err != nil && errors.Is(err, adpwsh.ErrReplication)
}

// errorDiagnostics renders a library error for Terraform.
func errorDiagnostics(op, resourceType string, err error) diag.Diagnostics {
	var diags diag.Diagnostics
	if err == nil {
		return diags
	}
	summary, detail := renderError(op, resourceType, err)
	diags.AddError(summary, detail)
	return diags
}

// attributeErrorDiagnostics is the same rendering, attached to the attribute
// the operator can actually change, so Terraform underlines the offending line
// instead of describing it.
func attributeErrorDiagnostics(op, resourceType string, err error, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	if err == nil {
		return diags
	}
	summary, detail := renderError(op, resourceType, err)
	diags.AddAttributeError(p, summary, detail)
	return diags
}

func renderError(op, resourceType string, err error) (summary, detail string) {
	var e *adpwsh.Error
	if !errors.As(err, &e) {
		return "Active Directory operation failed", fmt.Sprintf("%s: %s", op, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s failed.\n\n", op)
	if e.ServerMessage != "" {
		fmt.Fprintf(&b, "The domain controller said: %s\n", e.ServerMessage)
	} else if e.Err != nil {
		fmt.Fprintf(&b, "%s\n", e.Err)
	}
	if e.Target != "" {
		fmt.Fprintf(&b, "Target: %s\n", e.Target)
	} else if e.Identity != "" {
		fmt.Fprintf(&b, "Identity: %s\n", e.Identity)
	}

	switch e.Kind {
	case adpwsh.KindAlreadyExists:
		summary = "Object already exists in Active Directory"
		if e.Tombstoned {
			b.WriteString("\nThe name is held by a **deleted object** in the Deleted Objects " +
				"container, which the Recycle Bin keeps until its lifetime expires. Recreating it " +
				"is not possible while that object holds the name.\n\n" +
				"Restore it with Restore-ADObject, or wait for the deleted-object lifetime to pass.\n")
			break
		}
		b.WriteString("\nTo adopt the existing object into Terraform state instead of creating a " +
			"second one, add an import block and re-run plan:\n\n")
		fmt.Fprintf(&b, "import {\n  to = %s.<name>\n  id = %q\n}\n", resourceType, importIDFor(e))
	case adpwsh.KindNotFound:
		summary = "Object not found in Active Directory"
	case adpwsh.KindDenied:
		summary = "Access denied by Active Directory"
		b.WriteString("\nThe account this provider authenticates as does not have rights on the " +
			"target. Delegate the required permission on the container rather than widening the " +
			"account's domain-wide rights.\n")
	case adpwsh.KindConstraint:
		summary = "Active Directory refused the operation"
	case adpwsh.KindPassword:
		summary = "Password rejected by domain policy"
		b.WriteString("\nThe password never appears in this message, in state, or in the log.\n")
	case adpwsh.KindReferral:
		summary = "Active Directory returned a referral"
	case adpwsh.KindTransient:
		summary = "Active Directory was temporarily unavailable"
		b.WriteString("\nThe provider retried this operation and it kept failing.\n")
	case adpwsh.KindTransport:
		summary = "Cannot reach Active Directory"
		b.WriteString("\nThis is a transport problem — the SSH connection, the `pwsh` process, " +
			"or the ActiveDirectory module on the machine running it — not a refusal by " +
			"Active Directory.\n")
	case adpwsh.KindReplication:
		summary = "Replication wait timed out"
		b.WriteString("\nThe object was written successfully and the **state has been saved**; " +
			"only the wait for other domain controllers timed out. Re-running apply is safe. " +
			"Raise replication.timeout, or set replication.wait = false if the wait is not needed.\n")
	case adpwsh.KindTooManyResults:
		summary = "Too many results"
		b.WriteString("\nThe search matched more objects than its limit, and the provider errors " +
			"rather than silently returning a truncated set. Narrow the filter (container, scope, " +
			"filter_by or ldap_filter), or raise max_results if you really do want them all.\n")
	default:
		summary = "Active Directory operation failed"
		b.WriteString("\nThe provider does not recognise this condition and deliberately does not " +
			"guess: it is reported verbatim rather than retried.\n")
	}

	if e.ExceptionType != "" {
		fmt.Fprintf(&b, "\nException: %s", e.ExceptionType)
		if e.Code != 0 {
			fmt.Fprintf(&b, " (error code %#x)", e.Code)
		}
		b.WriteString("\n")
	}
	if e.FQID != "" {
		fmt.Fprintf(&b, "FullyQualifiedErrorId: %s\n", e.FQID)
	}
	return summary, b.String()
}

// importIDFor picks the most useful identity for the import block: the DN AD
// reported, falling back to whatever the operation was given.
func importIDFor(e *adpwsh.Error) string {
	if e.Target != "" {
		return e.Target
	}
	if i := strings.IndexByte(e.Identity, ':'); i >= 0 {
		return e.Identity[i+1:]
	}
	return e.Identity
}
