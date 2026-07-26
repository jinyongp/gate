package release

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	got, err := ParseOptions([]string{"", "-n", "--yes", "--since", "v1.2.3", "minor"})
	if err != nil {
		t.Fatal(err)
	}
	want := Options{
		DryRun:   true,
		AutoPush: true,
		TagInput: "minor",
		Since:    "v1.2.3",
		SinceSet: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}
}

func TestParseOptionsSupportsLegacyTagAssignmentAndLastValue(t *testing.T) {
	got, err := ParseOptions([]string{"patch", "tag=v2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got.TagInput != "v2.0.0" {
		t.Fatalf("tag input = %q", got.TagInput)
	}
}

func TestParseOptionsRejectsInvalidInputs(t *testing.T) {
	for _, args := range [][]string{
		{"--since"},
		{"wat"},
		{"v01.2.3"},
		{"v1.2"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, err := ParseOptions(args); err == nil {
				t.Fatalf("ParseOptions(%q) succeeded", args)
			}
		})
	}
}
