// Package ui holds the terminal helpers: colored messages, confirmation
// prompts and the editor.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/munepi/ctango/internal/submit"
)

// IsTerminal reports whether f is a character device (a TTY).
func IsTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// IO bundles the streams used by the commands, so that tests can replace them.
type IO struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	// InteractiveIn is true when In is a terminal (prompts and editor allowed).
	InteractiveIn bool
	// Color enables ANSI colors on ErrOut.
	Color bool
}

// System returns IO bound to the process streams.
func System() *IO {
	return &IO{
		In:            os.Stdin,
		Out:           os.Stdout,
		ErrOut:        os.Stderr,
		InteractiveIn: IsTerminal(os.Stdin) && IsTerminal(os.Stderr),
		Color:         IsTerminal(os.Stderr) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
	}
}

const (
	red    = "\x1b[31m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
	green  = "\x1b[32m"
	bold   = "\x1b[1m"
	reset  = "\x1b[0m"
)

func (o *IO) paint(code, s string) string {
	if !o.Color {
		return s
	}
	return code + s + reset
}

// Bold renders s in bold when colors are enabled.
func (o *IO) Bold(s string) string { return o.paint(bold, s) }

// Success prints a green check line to ErrOut.
func (o *IO) Success(format string, a ...any) {
	fmt.Fprintf(o.ErrOut, "%s %s\n", o.paint(green, "✓"), fmt.Sprintf(format, a...))
}

// Failure prints a red cross line to ErrOut.
func (o *IO) Failure(format string, a ...any) {
	fmt.Fprintf(o.ErrOut, "%s %s\n", o.paint(red, "X"), fmt.Sprintf(format, a...))
}

// PrintMessages writes the messages to ErrOut, one per line, colored by
// severity.
func (o *IO) PrintMessages(msgs []submit.Message) {
	for _, m := range msgs {
		label := m.Severity
		switch m.Severity {
		case submit.SeverityError:
			label = o.paint(red, label)
		case submit.SeverityWarning:
			label = o.paint(yellow, label)
		case submit.SeverityInfo:
			label = o.paint(cyan, label)
		}
		fmt.Fprintf(o.ErrOut, "  %s  %s\n", label, m.String())
	}
}

// Confirm asks a yes/no question; the default is no.
func (o *IO) Confirm(question string) (bool, error) {
	fmt.Fprintf(o.ErrOut, "%s [y/N] ", question)
	line, err := bufio.NewReader(o.In).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(o.ErrOut)
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// Editor returns the editor command from $CTANGO_EDITOR, $VISUAL or $EDITOR.
func Editor() string {
	for _, v := range []string{"CTANGO_EDITOR", "VISUAL", "EDITOR"} {
		if e := os.Getenv(v); e != "" {
			return e
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}

// Edit opens text in the user's editor and returns the result. Lines
// starting with "#" are removed, as in git commit messages.
func (o *IO) Edit(text string) (string, error) {
	f, err := os.CreateTemp("", "ctango-*.txt")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	// Run through the shell so that EDITOR="code --wait" works.
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", Editor()+" "+f.Name())
	} else {
		cmd = exec.Command("sh", "-c", Editor()+` "$1"`, "sh", f.Name())
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor: %w", err)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "#") {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), nil
}
