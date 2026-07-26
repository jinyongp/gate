package devcmd

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"

	"gate/internal/devtool/runner"
)

type forbiddenDocumentationPattern struct {
	message string
	pattern *regexp.Regexp
}

var forbiddenDocumentationPatterns = []forbiddenDocumentationPattern{
	{
		message: "docs/spec.md must not contain shell command fences; put command examples in docs/usage.md.",
		pattern: regexp.MustCompile("^```bash$"),
	},
	{
		message: "docs/spec.md must not contain exact gate command invocations; put command syntax in docs/usage.md.",
		pattern: regexp.MustCompile("`gate [a-z][a-z0-9-]*"),
	},
	{
		message: "docs/spec.md must not contain exact CLI long flags; put flags in docs/usage.md.",
		pattern: regexp.MustCompile("`--[a-z][a-z0-9-]*"),
	},
	{
		message: "docs/spec.md must not contain numeric exit-code references; put exit codes in docs/usage.md.",
		pattern: regexp.MustCompile("exit code `?[0-9]"),
	},
	{
		message: "docs/spec.md must not contain exit-code mapping table rows; put exit codes in docs/usage.md.",
		pattern: regexp.MustCompile("^\\| `[0-9]` \\|"),
	},
	{
		message: "docs/spec.md must not contain auth status value lists; put output semantics in docs/usage.md.",
		pattern: regexp.MustCompile("off`, `active"),
	},
	{
		message: "docs/spec.md must not mention the AUTH column; put output fields in docs/usage.md.",
		pattern: regexp.MustCompile("AUTH column"),
	},
	{
		message: "docs/spec.md must not mention auth_status; put output fields in docs/usage.md.",
		pattern: regexp.MustCompile("auth_status"),
	},
	{
		message: "docs/spec.md must not document JSON-mode error details; put output semantics in docs/usage.md.",
		pattern: regexp.MustCompile("JSON-mode errors"),
	},
	{
		message: "docs/spec.md must not contain text output examples; put output examples in docs/usage.md.",
		pattern: regexp.MustCompile("^removed "),
	},
	{
		message: "docs/spec.md must not contain JSON output examples; put output examples in docs/usage.md.",
		pattern: regexp.MustCompile(`^\{"scope":`),
	},
}

func (service *Service) docsCheck(ctx context.Context) error {
	spec := service.Getenv("DOCS_CHECK_SPEC")
	if spec == "" {
		spec = "docs/spec.md"
	}
	content, err := service.ReadFile(service.repositoryPath(spec))
	if err != nil {
		fmt.Fprintf(service.Err, "docs-check: missing spec file: %s\n", spec)
		return &reportedError{err: err, code: 1}
	}
	lines, err := documentationLines(content)
	if err != nil {
		return fmt.Errorf("read documentation lines: %w", err)
	}
	failed := false
	for _, forbidden := range forbiddenDocumentationPatterns {
		var matches []string
		for index, line := range lines {
			if forbidden.pattern.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%d:%s", index+1, line))
			}
		}
		if len(matches) == 0 {
			continue
		}
		failed = true
		fmt.Fprintf(service.Err, "docs-check: %s\n", forbidden.message)
		fmt.Fprintln(service.Err, strings.Join(matches, "\n"))
	}
	if err := service.stream(ctx, runner.Command{
		Name: "go",
		Args: []string{
			"test",
			"./cmd/gate",
			"-run",
			"TestUsageQuickReferenceMatchesPublicHelp",
			"-count=1",
		},
	}); err != nil {
		return err
	}
	if failed {
		return &reportedError{err: fmt.Errorf("documentation boundaries failed"), code: 1}
	}
	return nil
}

func documentationLines(content []byte) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
