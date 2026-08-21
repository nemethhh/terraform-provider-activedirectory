package provider

import "testing"

func TestCompileFilter(t *testing.T) {
	if got := compileFilter(nil, ""); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := compileFilter(map[string]string{"department": "Sales"}, ""); got != "(department=Sales)" {
		t.Fatalf("single equality = %q", got)
	}
	// Sorted key order for stable plans.
	if got := compileFilter(map[string]string{"b": "2", "a": "1"}, ""); got != "(&(a=1)(b=2))" {
		t.Fatalf("sorted equality = %q", got)
	}
	if got := compileFilter(nil, "(name=app-*)"); got != "(name=app-*)" {
		t.Fatalf("raw only = %q", got)
	}
	if got := compileFilter(map[string]string{"a": "1"}, "(name=app-*)"); got != "(&(a=1)(name=app-*))" {
		t.Fatalf("both = %q", got)
	}
	// Escaping goes through the library.
	if got := compileFilter(map[string]string{"cn": "R&D (x)"}, ""); got != `(cn=R&D \28x\29)` {
		t.Fatalf("escaping = %q", got)
	}
}
