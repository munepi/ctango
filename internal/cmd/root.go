// Package cmd implements the ctan command line.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/munepi/ctango/internal/ctanapi"
	"github.com/munepi/ctango/internal/submit"
	"github.com/munepi/ctango/internal/ui"
)

// ExitError carries an exit code without printing anything more; the
// command has already reported the problem.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Silent failure with exit status 1.
var errFailed = &ExitError{Code: 1}

// Factory gives commands access to shared dependencies.
type Factory struct {
	IO        *ui.IO
	Version   string
	userAgent string
}

func (f *Factory) UserAgent() string {
	if f.userAgent == "" {
		f.userAgent = "ctango/" + f.Version + " (+https://github.com/munepi/ctango)"
	}
	return f.userAgent
}

func (f *Factory) SubmitClient() *submit.Client { return submit.NewClient(f.UserAgent()) }
func (f *Factory) APIClient() *ctanapi.Client   { return ctanapi.NewClient(f.UserAgent()) }

// NewRootCmd builds the command tree.
func NewRootCmd(f *Factory) *cobra.Command {
	root := &cobra.Command{
		Use:   "ctango <command> [flags]",
		Short: "Work with CTAN from the command line",
		Long: `Work with CTAN from the command line.

Package metadata lives in ctan.toml (create it with "ctango init");
"ctango upload" sends the archive and the metadata to CTAN.`,
		Example: `  $ ctango init
  $ ctango validate
  $ ctango upload --announcement-file NEWS.txt`,
		Version:       f.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(f.IO.In)
	root.SetOut(f.IO.Out)
	root.SetErr(f.IO.ErrOut)
	root.SetVersionTemplate("ctango version {{.Version}}\n")

	root.AddCommand(
		newInitCmd(f),
		newValidateCmd(f),
		newUploadCmd(f),
		newFieldsCmd(f),
		newPkgCmd(f),
	)
	return root
}

// Execute runs the command line and returns the process exit code.
func Execute(f *Factory, args []string) int {
	root := NewRootCmd(f)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			return exit.Code
		}
		fmt.Fprintf(f.IO.ErrOut, "ctango: %v\n", err)
		return 1
	}
	return 0
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeRawJSON pretty-prints a raw JSON body, falling back to the bytes.
func writeRawJSON(w io.Writer, raw []byte) error {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		_, err := w.Write(raw)
		return err
	}
	return writeJSON(w, v)
}
