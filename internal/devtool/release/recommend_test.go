package release

import "testing"

func TestRecommendBump(t *testing.T) {
	tests := []struct {
		name     string
		subjects []string
		messages string
		want     string
	}{
		{name: "breaking subject", subjects: []string{"feat(cli)!: remove flag"}, want: "major"},
		{name: "breaking body", subjects: []string{"refactor: cleanup"}, messages: "BREAKING CHANGE: config moved", want: "major"},
		{name: "feature", subjects: []string{"fix: bug", "feat(proxy): add mode"}, want: "minor"},
		{name: "patch", subjects: []string{"fix: bug", "docs: clarify"}, want: "patch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recommendBump(test.subjects, test.messages); got != test.want {
				t.Fatalf("recommendation = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRecommendationReason(t *testing.T) {
	lines := []string{"abc1234 fix: bug", "def5678 feat(cli): add command"}
	if got := recommendationReason("minor", lines, ""); got != lines[1] {
		t.Fatalf("minor reason = %q", got)
	}
	if got := recommendationReason("patch", lines, ""); got != lines[0] {
		t.Fatalf("patch reason = %q", got)
	}
	if got := recommendationReason("major", lines, "BREAKING CHANGE: format changed"); got != "BREAKING CHANGE: format changed" {
		t.Fatalf("major reason = %q", got)
	}
}
