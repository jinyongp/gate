package release

import "testing"

func TestVersionNext(t *testing.T) {
	version, err := ParseVersion("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for bump, want := range map[string]string{
		"patch": "v1.2.4",
		"minor": "v1.3.0",
		"major": "v2.0.0",
	} {
		t.Run(bump, func(t *testing.T) {
			next, nextErr := version.Next(bump)
			if nextErr != nil {
				t.Fatal(nextErr)
			}
			if got := next.String(); got != want {
				t.Fatalf("next = %q, want %q", got, want)
			}
		})
	}
}

func TestParseVersionRejectsLooseSemver(t *testing.T) {
	for _, tag := range []string{"1.2.3", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2", "v1.2.3.4"} {
		if _, err := ParseVersion(tag); err == nil {
			t.Fatalf("ParseVersion(%q) succeeded", tag)
		}
	}
}

func TestResolveTag(t *testing.T) {
	if got, err := resolveTag("v1.2.3", "minor"); err != nil || got != "v1.3.0" {
		t.Fatalf("resolve bump = %q, %v", got, err)
	}
	if got, err := resolveTag("v1.2.3", "v3.0.0"); err != nil || got != "v3.0.0" {
		t.Fatalf("resolve explicit = %q, %v", got, err)
	}
}
