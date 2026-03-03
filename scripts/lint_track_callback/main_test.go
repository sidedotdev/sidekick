package main

import (
	"testing"
)

func TestFindViolations_Violations(t *testing.T) {
	t.Parallel()

	violations, err := findViolations([]string{"./testdata/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Filter to only violations from violations.go
	var filtered []violation
	for _, v := range violations {
		if containsSubstr(v.Pos.Filename, "violations.go") && !containsSubstr(v.Pos.Filename, "no_violations.go") {
			filtered = append(filtered, v)
		}
	}

	if len(filtered) != 6 {
		t.Errorf("expected 6 violations, got %d:", len(filtered))
		for _, v := range filtered {
			t.Logf("  %s: %q in %s", v.Pos, v.VarName, v.FuncName)
		}
	}

	// Verify specific variable names are caught
	varNames := make(map[string]bool)
	for _, v := range filtered {
		varNames[v.VarName] = true
	}
	for _, expected := range []string{"dCtx", "eCtx", "actionCtx", "myCtx"} {
		if !varNames[expected] {
			t.Errorf("expected violation for variable %q not found", expected)
		}
	}
}

func TestFindViolations_NoViolations(t *testing.T) {
	t.Parallel()

	violations, err := findViolations([]string{"./testdata/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Filter to only violations from no_violations.go
	var filtered []violation
	for _, v := range violations {
		if containsSubstr(v.Pos.Filename, "no_violations.go") {
			filtered = append(filtered, v)
		}
	}

	if len(filtered) != 0 {
		t.Errorf("expected 0 violations from no_violations.go, got %d:", len(filtered))
		for _, v := range filtered {
			t.Logf("  %s: %q in %s", v.Pos, v.VarName, v.FuncName)
		}
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsAny(s, sub))
}

func containsAny(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}