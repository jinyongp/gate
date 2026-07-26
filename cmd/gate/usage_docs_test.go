package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gate/internal/cli"
)

var (
	quickReferenceCommandRE = regexp.MustCompile("^\\|\\s+`([^`]+)`")
	flagTokenRE             = regexp.MustCompile(`(^|[,\s\[\|])(--[A-Za-z][A-Za-z0-9-]*|-[A-Za-z])`)
	helpFlagSeparatorRE     = regexp.MustCompile(`\s{2,}`)
)

type quickReferenceRow struct {
	signature string
	paths     []string
	flags     map[string]bool
}

func TestUsageQuickReferenceMatchesPublicHelp(t *testing.T) {
	rows := readUsageQuickReferenceRows(t)
	rowsByPath := make(map[string]quickReferenceRow)
	for _, row := range rows {
		for _, path := range row.paths {
			if existing, ok := rowsByPath[path]; ok {
				t.Fatalf("duplicate Quick Reference command path %q in %q and %q", path, existing.signature, row.signature)
			}
			rowsByPath[path] = row
		}
	}

	expected := expectedUsageCommandPaths(t)
	expectedSet := stringSet(expected)
	var missingRows []string
	for _, path := range expected {
		if _, ok := rowsByPath[path]; !ok {
			missingRows = append(missingRows, path)
		}
	}

	var staleRows []string
	for path := range rowsByPath {
		if !expectedSet[path] {
			staleRows = append(staleRows, path)
		}
	}
	sort.Strings(staleRows)

	if len(missingRows) > 0 || len(staleRows) > 0 {
		t.Fatalf("Quick Reference command rows drifted\nmissing: %v\nstale: %v", missingRows, staleRows)
	}

	for _, path := range expected {
		row := rowsByPath[path]
		helpFlags := commandHelpFlags(t, path)
		missingFlags := missingStrings(sortedKeys(helpFlags), row.flags)
		staleFlags := missingStrings(sortedKeys(row.flags), helpFlags)
		if len(missingFlags) > 0 || len(staleFlags) > 0 {
			t.Errorf("%s Quick Reference flags drifted\nmissing: %v\nstale: %v\nrow: %s", path, missingFlags, staleFlags, row.signature)
		}
	}
}

func readUsageQuickReferenceRows(t *testing.T) []quickReferenceRow {
	t.Helper()

	path := os.Getenv("GATE_USAGE_DOCS_PATH")
	if path == "" {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("locate test file")
		}
		path = filepath.Join(filepath.Dir(file), "..", "..", "docs", "usage.md")
	}

	// #nosec G703 -- test-only docs path override is used to validate drift failures.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read usage docs: %v", err)
	}

	var rows []quickReferenceRow
	inQuickReference := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.TrimSpace(line) == "## Quick Reference":
			inQuickReference = true
			continue
		case inQuickReference && strings.HasPrefix(line, "## "):
			inQuickReference = false
		}
		if !inQuickReference {
			continue
		}
		match := quickReferenceCommandRE.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		signature := strings.ReplaceAll(match[1], `\|`, "|")
		rows = append(rows, quickReferenceRow{
			signature: signature,
			paths:     commandPathVariants(signature),
			flags:     extractFlagSet(signature),
		})
	}

	if len(rows) == 0 {
		t.Fatalf("no Quick Reference command rows found in %s", path)
	}
	return rows
}

func commandPathVariants(signature string) []string {
	fields := strings.Fields(signature)
	if len(fields) == 0 || fields[0] != "gate" {
		return nil
	}

	var tokenAlternatives [][]string
	for i, field := range fields {
		if i > 0 && (strings.HasPrefix(field, "[") || strings.HasPrefix(field, "<") || strings.HasPrefix(field, "-")) {
			break
		}
		field = strings.Trim(field, ",")
		tokenAlternatives = append(tokenAlternatives, strings.Split(field, "|"))
	}

	paths := []string{""}
	for _, alternatives := range tokenAlternatives {
		var next []string
		for _, prefix := range paths {
			for _, alternative := range alternatives {
				if prefix == "" {
					next = append(next, alternative)
				} else {
					next = append(next, prefix+" "+alternative)
				}
			}
		}
		paths = next
	}
	sort.Strings(paths)
	return paths
}

func expectedUsageCommandPaths(t *testing.T) []string {
	t.Helper()

	paths := make(map[string]bool)
	for _, spec := range cli.Specs {
		base := "gate " + spec.Name
		children := helpCommandNames(t, base)
		if len(children) == 0 || spec.Name == "expose" {
			paths[base] = true
		}
		for _, child := range children {
			paths[base+" "+child] = true
		}
	}
	// Usage documents the full supported CLI, including commands hidden from
	// the current host's help. Keep Linux-only setup covered on every CI host.
	paths["gate daemon setup"] = true
	return sortedKeys(paths)
}

func helpCommandNames(t *testing.T, path string) []string {
	t.Helper()
	var names []string
	for _, line := range helpSectionLines(commandHelp(t, path), "COMMANDS") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	return names
}

func commandHelpFlags(t *testing.T, path string) map[string]bool {
	t.Helper()

	out := make(map[string]bool)
	for _, line := range helpSectionLines(commandHelp(t, path), "FLAGS") {
		for flagName := range extractFlagSet(helpFlagColumn(line)) {
			out[flagName] = true
		}
	}
	return out
}

func commandHelp(t *testing.T, path string) string {
	t.Helper()

	args := strings.Fields(strings.TrimPrefix(path, "gate "))
	args = append(args, "-h")

	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("%s -h exit = %d; stderr=%s", path, code, stderr.String())
	}
	return stdout.String()
}

func helpSectionLines(help, section string) []string {
	var out []string
	inSection := false
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == section:
			inSection = true
			continue
		case inSection && trimmed == "":
			if len(out) > 0 {
				return out
			}
			continue
		case !inSection:
			continue
		}

		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func extractFlagSet(text string) map[string]bool {
	flags := make(map[string]bool)
	for _, match := range flagTokenRE.FindAllStringSubmatch(text, -1) {
		flags[match[2]] = true
	}
	return flags
}

func helpFlagColumn(line string) string {
	parts := helpFlagSeparatorRE.Split(line, 2)
	return parts[0]
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func missingStrings(values []string, known map[string]bool) []string {
	var missing []string
	for _, value := range values {
		if !known[value] {
			missing = append(missing, value)
		}
	}
	return missing
}

func TestCommandPathVariants(t *testing.T) {
	tests := map[string][]string{
		"gate expose [--via local|lan] <service>": {"gate expose"},
		"gate completion bash|zsh|fish":           {"gate completion bash", "gate completion fish", "gate completion zsh"},
		"gate skill path|print":                   {"gate skill path", "gate skill print"},
	}
	for signature, want := range tests {
		if got := commandPathVariants(signature); !reflect.DeepEqual(got, want) {
			t.Fatalf("commandPathVariants(%q) = %v, want %v", signature, got, want)
		}
	}
}
