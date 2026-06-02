package uitest

import (
	"testing"

	"gate/internal/ui/policy"
)

// ClearColorEnv removes colour-related environment overrides for tests that
// need deterministic default presentation output.
func ClearColorEnv(t testing.TB) {
	t.Helper()
	policy.ClearColorEnv(t.Setenv)
}

// ForceColor enables styled output for tests without exposing the env contract
// outside the ui package tree.
func ForceColor(t testing.TB) {
	t.Helper()
	policy.ForceColorEnv(t.Setenv)
}

// DisableColor disables styled output for tests without exposing the env
// contract outside the ui package tree.
func DisableColor(t testing.TB) {
	t.Helper()
	policy.DisableColorEnv(t.Setenv)
}
