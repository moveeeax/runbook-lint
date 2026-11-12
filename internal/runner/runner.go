package runner

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"time"
)

// Options configures a run.
type Options struct {
	// Runbook is the path shown in prompts and the transcript.
	Runbook string
	// Vars are placeholder values given up front (--var NAME=VALUE). Missing
	// ones are prompted for once, when a step first needs them.
	Vars map[string]string
	// From skips steps with a lower Index (resume a procedure halfway).
	From int
	// Only keeps steps whose step= tag is in the list. Empty keeps all.
	Only []string
	// DryRun prints the plan and runs nothing.
	DryRun bool
	// KeepGoing continues after a failed step instead of stopping.
	KeepGoing bool
	// Editor is the command used for edit-before-running; empty reads the
	// replacement from the prompt input instead.
	Editor string

	// In answers prompts. Out and Err receive prompts and command output.
	In       io.Reader
	Out, Err io.Writer
	// Stdin is passed to commands. nil gives them an empty stdin.
	Stdin io.Reader
	// Transcript, when set, receives a record of every step decision and result.
	Transcript io.Writer
	// Now is the clock for transcript timestamps. Injectable for tests.
	Now func() time.Time
}

// ErrAborted is returned when the operator aborts the procedure.
var ErrAborted = errors.New("aborted")

// StepError reports the step that stopped the run.
type StepError struct {
	Step     Step
	ExitCode int
	Err      error
}

func (e *StepError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("step %d failed: %v", e.Step.Index, e.Err)
	}
	return fmt.Sprintf("step %d exited with code %d", e.Step.Index, e.ExitCode)
}

// Select applies From and Only to the parsed steps.
func Select(steps []Step, from int, only []string) []Step {
	keep := map[string]bool{}
	for _, o := range only {
		keep[o] = true
	}
	var out []Step
	for _, s := range steps {
		if s.Index < from {
			continue
		}
		if len(keep) > 0 && !keep[s.Tags["step"]] {
			continue
		}
		out = append(out, s)
	}
	return out
}

type run struct {
	opts  Options
	in    *bufio.Reader
	vars  map[string]string
	total int
}

// Run executes the selected steps of a runbook. It returns nil when every
// step ran or was skipped, ErrAborted when the operator aborted, and a
// *StepError when a step failed and KeepGoing is off. With KeepGoing, the
// last failure is returned after all steps are done.
func Run(steps []Step, opts Options) error {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Transcript == nil {
		opts.Transcript = io.Discard
	}
	if opts.In == nil {
		opts.In = strings.NewReader("")
	}
	r := &run{opts: opts, in: bufio.NewReader(opts.In), vars: map[string]string{}}
	for k, v := range opts.Vars {
		r.vars[k] = v
	}
	selected := Select(steps, opts.From, opts.Only)
	r.total = len(steps)

	if opts.DryRun {
		r.plan(selected)
		return nil
	}

	r.record("# runbook-lint run transcript\nrunbook: %s\nstarted: %s\n", opts.Runbook, r.stamp())
	if opts.From > 1 {
		r.record("from: step %d\n", opts.From)
	}
	if len(opts.Only) > 0 {
		r.record("only: step=%s\n", strings.Join(opts.Only, ","))
	}

	var lastFail error
	for _, s := range selected {
		err := r.step(s)
		var se *StepError
		switch {
		case err == nil:
		case errors.As(err, &se) && opts.KeepGoing:
			lastFail = err
		default:
			r.record("\nfinished: %s (%v)\n", r.stamp(), err)
			return err
		}
	}
	r.record("\nfinished: %s\n", r.stamp())
	return lastFail
}

func (r *run) plan(steps []Step) {
	fmt.Fprintf(r.opts.Out, "%s: %d step(s) selected of %d\n", r.opts.Runbook, len(steps), r.total)
	for _, s := range steps {
		fmt.Fprintf(r.opts.Out, "\n%s\n", r.header(s))
		script := Substitute(s.Script, r.vars)
		fmt.Fprint(r.opts.Out, indent(script))
		var missing []string
		for _, name := range Placeholders(script) {
			if _, set := os.LookupEnv(name); !set {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(r.opts.Out, "  (will prompt for %s)\n", strings.Join(missing, ", "))
		}
	}
}

func (r *run) header(s Step) string {
	h := fmt.Sprintf("step %d/%d (%s, line %d)", s.Index, r.total, s.Lang, s.Line)
	if len(s.Tags) > 0 {
		keys := make([]string, 0, len(s.Tags))
		for k := range s.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"="+s.Tags[k])
		}
		h += " [" + strings.Join(parts, " ") + "]"
	}
	return h
}

func (r *run) step(s Step) error {
	script, err := r.fill(s.Script)
	if err != nil {
		return err
	}
	edited := false
	for {
		fmt.Fprintf(r.opts.Out, "\n%s\n%s", r.header(s), indent(script))
		answer, err := r.ask("[r]un, [s]kip, [e]dit, [a]bort? ")
		if err != nil {
			r.record("\n--- %s at %s\naborted: no answer (%v)\n", r.header(s), r.stamp(), err)
			return ErrAborted
		}
		switch strings.ToLower(answer) {
		case "r", "run":
			return r.exec(s, script, edited)
		case "s", "skip":
			r.record("\n--- %s at %s\nskipped\n$ %s", r.header(s), r.stamp(), script)
			return nil
		case "e", "edit":
			next, err := r.edit(script)
			if err != nil {
				fmt.Fprintf(r.opts.Err, "edit failed: %v\n", err)
				continue
			}
			if next != script {
				script, edited = next, true
			}
		case "a", "abort", "q", "quit":
			r.record("\n--- %s at %s\naborted by operator\n", r.header(s), r.stamp())
			return ErrAborted
		}
	}
}

// fill substitutes placeholders, prompting once for any value not given
// with --var and not set in the environment.
func (r *run) fill(script string) (string, error) {
	for _, name := range Placeholders(script) {
		if _, ok := r.vars[name]; ok {
			continue
		}
		if _, set := os.LookupEnv(name); set {
			continue // the shell expands it
		}
		v, err := r.ask(fmt.Sprintf("value for %s: ", name))
		if err != nil {
			return "", ErrAborted
		}
		r.vars[name] = v
		r.record("var: %s set at prompt\n", name)
	}
	return Substitute(script, r.vars), nil
}

func (r *run) exec(s Step, script string, edited bool) error {
	start := r.opts.Now()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(s.Lang, "-c", script)
	cmd.Stdin = r.opts.Stdin
	cmd.Stdout = io.MultiWriter(r.opts.Out, &stdout)
	cmd.Stderr = io.MultiWriter(r.opts.Err, &stderr)

	// Ctrl-C reaches the command (same process group) and must not kill the
	// runner before the result is written to the transcript.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	runErr := cmd.Run()
	signal.Stop(sig)

	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	case runErr != nil:
		code = -1
	}

	note := ""
	if edited {
		note = " (edited before running)"
	}
	r.record("\n--- %s at %s%s\n$ %s", r.header(s), start.UTC().Format(time.RFC3339), note, script)
	r.record("exit: %d  duration: %s\n", code, r.opts.Now().Sub(start).Round(time.Millisecond))
	r.record("stdout:\n%s", block(stdout.String()))
	r.record("stderr:\n%s", block(stderr.String()))

	switch {
	case runErr != nil && code == -1:
		return &StepError{Step: s, ExitCode: code, Err: runErr}
	case code != 0:
		fmt.Fprintf(r.opts.Err, "step %d exited with code %d\n", s.Index, code)
		return &StepError{Step: s, ExitCode: code}
	}
	return nil
}

func (r *run) edit(script string) (string, error) {
	if r.opts.Editor == "" {
		fmt.Fprintln(r.opts.Out, "enter the replacement command, end with a line containing only \".\":")
		var b strings.Builder
		for {
			line, err := r.in.ReadString('\n')
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "." {
				break
			}
			b.WriteString(trimmed + "\n")
			if err != nil {
				return "", err
			}
		}
		return b.String(), nil
	}

	f, err := os.CreateTemp("", "runbook-step-*.sh")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	// Run through sh so EDITOR may carry arguments ("code --wait").
	cmd := exec.Command("sh", "-c", r.opts.Editor+` "$1"`, "sh", f.Name())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r.opts.Stdin, r.opts.Out, r.opts.Err
	if err := cmd.Run(); err != nil {
		return "", err
	}
	out, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (r *run) ask(prompt string) (string, error) {
	fmt.Fprint(r.opts.Out, prompt)
	line, err := r.in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(r.opts.Out)
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (r *run) record(format string, args ...any) {
	fmt.Fprintf(r.opts.Transcript, format, args...)
}

func (r *run) stamp() string {
	return r.opts.Now().UTC().Format(time.RFC3339)
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + strings.TrimRight(line, "\n") + "\n")
	}
	return b.String()
}

func block(s string) string {
	if s == "" {
		return "  (empty)\n"
	}
	return indent(s)
}
