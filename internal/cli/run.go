package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/moveeeax/runbook-lint/internal/runner"
)

// runRunbook implements `runbook-lint run`. Exit codes:
//
//	0 = every selected step ran or was skipped, 1 = a step failed or the
//	operator aborted, 2 = usage/IO error.
func runRunbook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("runbook-lint run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vars := map[string]string{}
	fs.Func("var", "set placeholder `NAME=VALUE` used as ${NAME} in steps (repeatable)", func(s string) error {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return errors.New("want NAME=VALUE")
		}
		vars[k] = v
		return nil
	})
	var only []string
	fs.Func("only", "run only steps tagged step=`TAG` in the fence info string (comma-separated or repeatable)", func(s string) error {
		for _, t := range strings.Split(s, ",") {
			if t = strings.TrimSpace(t); t != "" {
				only = append(only, t)
			}
		}
		return nil
	})
	from := fs.Int("from", 1, "start at step `N`, to resume a procedure stopped halfway")
	dryRun := fs.Bool("dry-run", false, "print the selected steps and exit without running anything")
	transcriptPath := fs.String("transcript", "", "append a timestamped record of every step to `FILE`")
	keepGoing := fs.Bool("keep-going", false, "continue after a step exits non-zero")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: runbook-lint run [flags] <runbook.md>\n\n")
		fmt.Fprintf(stderr, "Run the fenced bash and sh blocks of a runbook in document order. Every\n")
		fmt.Fprintf(stderr, "step asks for confirmation: run, skip, edit before running, or abort.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)
	if *dryRun && *transcriptPath != "" {
		fmt.Fprintln(stderr, "error: --transcript has nothing to record with --dry-run")
		return 2
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	steps := runner.ParseSteps(src)
	if len(steps) == 0 {
		fmt.Fprintf(stderr, "error: %s has no fenced bash or sh blocks\n", path)
		return 2
	}
	if *from < 1 || *from > len(steps) {
		fmt.Fprintf(stderr, "error: --from %d is out of range, %s has %d step(s)\n", *from, path, len(steps))
		return 2
	}
	if len(runner.Select(steps, *from, only)) == 0 {
		fmt.Fprintln(stderr, "error: no steps match --from/--only")
		return 2
	}

	opts := runner.Options{
		Runbook:   path,
		Vars:      vars,
		From:      *from,
		Only:      only,
		DryRun:    *dryRun,
		KeepGoing: *keepGoing,
		Editor:    editor(),
		In:        stdin,
		Out:       stdout,
		Err:       stderr,
	}
	// Commands share the terminal only when the prompts read from it too.
	if f, ok := stdin.(*os.File); ok {
		opts.Stdin = f
	}
	if *transcriptPath != "" {
		// Append, so a run resumed with --from lands in the same transcript.
		// 0600: command output can carry secrets.
		t, err := os.OpenFile(*transcriptPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		defer t.Close()
		opts.Transcript = t
	}

	err = runner.Run(steps, opts)
	var se *runner.StepError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, runner.ErrAborted):
		fmt.Fprintln(stderr, "aborted")
		return 1
	case errors.As(err, &se):
		fmt.Fprintf(stderr, "stopped: %v; resume with --from %d\n", err, se.Step.Index)
		return 1
	default:
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
}

// editor returns the command for edit-before-running, following the usual
// VISUAL then EDITOR order. Empty means the replacement is typed at the prompt.
func editor() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}
