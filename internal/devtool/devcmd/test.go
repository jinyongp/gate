package devcmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"gate/internal/devtool/runner"
	"gate/internal/ui"
)

func (service *Service) goTest(ctx context.Context, cover bool, extraArgs []string) error {
	args := []string{"test", "-race"}
	for _, arg := range extraArgs {
		if !strings.HasPrefix(arg, "-count=") {
			args = append(args, arg)
		}
	}
	args = append(args, "-count=1")
	if cover {
		args = append(args, "-cover")
	}
	args = append(args, "./...")
	var output bytes.Buffer
	err := service.Runner.Run(ctx, runner.Command{
		Name:   "go",
		Args:   args,
		Dir:    service.Dir,
		Stdin:  service.In,
		Stdout: &output,
		Stderr: service.Err,
	})
	formatted := formatGoTestOutput(output.String(), service.Out)
	if formatted != "" {
		fmt.Fprint(service.Out, formatted)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type testOutputRow struct {
	raw       string
	formatted bool
	status    string
	pkg       string
	elapsed   string
	detail    string
}

func formatGoTestOutput(output string, destination io.Writer) string {
	rows := parseTestOutput(output)
	statusWidth := 4
	packageWidth := 30
	elapsedWidth := 12
	for _, row := range rows {
		if !row.formatted {
			continue
		}
		statusWidth = maxWidth(statusWidth, len(row.status))
		packageWidth = maxWidth(packageWidth, len(row.pkg))
		elapsedWidth = maxWidth(elapsedWidth, len(row.elapsed))
	}
	var builder strings.Builder
	color := ui.ColorEnabled(destination)
	for _, row := range rows {
		if !row.formatted {
			builder.WriteString(row.raw)
			builder.WriteByte('\n')
			continue
		}
		status := row.status
		pkg := row.pkg
		elapsed := row.elapsed
		detail := row.detail
		if color {
			switch status {
			case "ok":
				status = ui.Tint(ui.Success, status)
			case "?":
				status = ui.Tint(ui.Warn, status)
			case "FAIL":
				status = ui.Tint(ui.Danger, status)
			}
			pkg = ui.Header.Render(pkg)
			if elapsed == "(cached)" || elapsed == "[no test files]" {
				elapsed = ui.Dim.Render(elapsed)
			}
			if strings.HasPrefix(detail, "coverage:") {
				detail = ui.Dim.Render(detail)
			}
		}
		fmt.Fprintf(
			&builder,
			"%s%s %s%s %s",
			status,
			padding(row.status, statusWidth),
			pkg,
			padding(row.pkg, packageWidth),
			elapsed,
		)
		if row.detail != "" {
			fmt.Fprintf(&builder, "%s %s", padding(row.elapsed, elapsedWidth), detail)
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func parseTestOutput(output string) []testOutputRow {
	output = strings.TrimRight(output, "\r\n")
	if output == "" {
		return nil
	}
	lines := strings.Split(output, "\n")
	rows := make([]testOutputRow, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			rows = append(rows, testOutputRow{raw: line})
			continue
		}
		status := strings.TrimSpace(fields[0])
		if status != "ok" && status != "?" && status != "FAIL" && status != "" {
			rows = append(rows, testOutputRow{raw: line})
			continue
		}
		detail := ""
		if len(fields) >= 4 {
			detail = fields[3]
		}
		elapsed := fields[2]
		if status == "" && strings.HasPrefix(detail, "coverage:") {
			status = "?"
			elapsed = "[no test files]"
		} else if status == "" {
			status = " "
		}
		rows = append(rows, testOutputRow{
			formatted: true,
			status:    status,
			pkg:       fields[1],
			elapsed:   elapsed,
			detail:    detail,
		})
	}
	return rows
}

func padding(value string, width int) string {
	return strings.Repeat(" ", maxWidth(0, width-len(value)))
}

func maxWidth(left, right int) int {
	if left > right {
		return left
	}
	return right
}
