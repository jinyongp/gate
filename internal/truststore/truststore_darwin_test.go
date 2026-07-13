//go:build darwin

package truststore

import "testing"

func TestIsRemoveTrustedCertNotFound(t *testing.T) {
	for _, output := range []string{
		"The specified item could not be found in the keychain.",
		"Certificate could not be found in the keychain",
	} {
		if !isRemoveTrustedCertNotFound([]byte(output)) {
			t.Fatalf("%q was not classified as missing", output)
		}
	}
	for _, output := range []string{"helper not found", "database not found", "permission denied"} {
		if isRemoveTrustedCertNotFound([]byte(output)) {
			t.Fatalf("%q was classified as missing", output)
		}
	}
}
