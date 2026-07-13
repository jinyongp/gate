package truststore

import "testing"

func TestIsNSSCertificateNotFound(t *testing.T) {
	t.Parallel()

	for _, output := range []string{
		"certutil: Could not find cert: gate",
		"certificate not found",
	} {
		if !isNSSCertificateNotFound([]byte(output)) {
			t.Fatalf("%q was not classified as a missing certificate", output)
		}
	}

	for _, output := range []string{
		"database not found",
		"SEC_ERROR_BAD_DATABASE: security library: bad database",
		"permission denied",
	} {
		if isNSSCertificateNotFound([]byte(output)) {
			t.Fatalf("%q was classified as a missing certificate", output)
		}
	}
}
