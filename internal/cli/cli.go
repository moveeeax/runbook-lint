// Package cli wires the runbook linter to a command line: it collects markdown
// files, runs the linter, and renders results as text or JSON.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moveeeax/runbook-lint/internal/linter"
)

// Version is the released binary version.
const Version = "0.1.0"

// Run executes the CLI and returns a process exit code:
//
//	0 = clean, 1 = at least one error-level finding, 2 = usage/IO error.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("runbook-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit findings as JSON")
	maxAge := fs.Int("max-age-days", 180, "warn on runbooks not reviewed within N days (0 disables)")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: runbook-lint [flags] <path>...\n\n")
		fmt.Fprintf(stderr, "Lint operational runbooks for required structure, frontmatter,\n")
		fmt.Fprintf(stderr, "staleness and broken internal links. Paths may be files or directories.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "runbook-lint "+Version)
		return 0
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		return 2
	}

	cfg := linter.DefaultConfig()
	cfg.MaxAgeDays = *maxAge
	cfg.Now = time.Now()

	files, err := collect(paths)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "no markdown files found in", strings.Join(paths, ", "))
		return 2
	}

	var all []linter.Finding
	for _, f := range files {
		found, err := linter.LintFile(f, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "error reading %s: %v\n", f, err)
			return 2
		}
		all = append(all, found...)
	}

	if *jsonOut {
		return renderJSON(stdout, all)
	}
	return renderText(stdout, len(files), all)
}

func renderJSON(w io.Writer, findings []linter.Finding) int {
	if findings == nil {
		findings = []linter.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(findings)
	return exitCode(findings)
}

func renderText(w io.Writer, nFiles int, findings []linter.Finding) int {
	if len(findings) == 0 {
		fmt.Fprintf(w, "✓ %d runbook(s) OK\n", nFiles)
		return 0
	}
	byFile := map[string][]linter.Finding{}
	var order []string
	for _, f := range findings {
		if _, seen := byFile[f.File]; !seen {
			order = append(order, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	sort.Strings(order)

	var errs, warns int
	for _, file := range order {
		fmt.Fprintf(w, "%s\n", file)
		for _, f := range byFile[file] {
			mark := "warning"
			if f.Level == linter.Error {
				mark = "error"
				errs++
			} else {
				warns++
			}
			fmt.Fprintf(w, "  %-7s [%s] %s\n", mark, f.Rule, f.Message)
		}
	}
	fmt.Fprintf(w, "\n%d error(s), %d warning(s) across %d file(s)\n", errs, warns, nFiles)
	return exitCode(findings)
}

func exitCode(findings []linter.Finding) int {
	for _, f := range findings {
		if f.Level == linter.Error {
			return 1
		}
	}
	return 0
}

// collect expands the given paths into a sorted, de-duplicated list of markdown
// files. Directories are walked recursively.
func collect(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	addFile := func(p string) {
		if isMarkdown(p) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			addFile(p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				addFile(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func isMarkdown(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".md" || ext == ".markdown"
}
