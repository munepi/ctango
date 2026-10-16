package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/munepi/ctango/internal/archive"
	"github.com/munepi/ctango/internal/config"
	"github.com/munepi/ctango/internal/ctanapi"
	"github.com/munepi/ctango/internal/submit"
)

type submitOptions struct {
	upload bool

	configPath       string
	version          string
	update           string
	announcement     string
	announcementFile string
	note             string
	noteFile         string
	uploader         string
	email            string
	dryRun           bool
	yes              bool
	jsonOut          bool
	skipChecks       bool
	force            bool

	// lookupMessages are findings from comparing with the package on CTAN.
	lookupMessages []submit.Message
}

func newValidateCmd(f *Factory) *cobra.Command {
	o := &submitOptions{}
	cmd := &cobra.Command{
		Use:   "validate [<archive>]",
		Short: "Check a submission with CTAN without uploading it",
		Long: `Check a submission with CTAN without uploading it.

The archive and the metadata from ctan.toml are sent to the CTAN
validation endpoint. Nothing is stored or published.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runSubmit(cmd, f, o, args) },
	}
	addSubmitFlags(cmd, o)
	return cmd
}

func newUploadCmd(f *Factory) *cobra.Command {
	o := &submitOptions{upload: true}
	cmd := &cobra.Command{
		Use:   "upload [<archive>]",
		Short: "Upload a package to CTAN",
		Long: `Upload a package to CTAN.

The archive is checked locally, validated by CTAN, and after
confirmation handed over to the CTAN team. Metadata is read from
ctan.toml; flags override it.

Without --announcement or --announcement-file, an editor is opened
for the announcement text (leave it empty for no announcement).`,
		Example: `  $ ctango upload
  $ ctango upload dist/foo.zip --version 1.2 -A NEWS.txt
  $ ctango upload --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runSubmit(cmd, f, o, args) },
	}
	addSubmitFlags(cmd, o)
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Upload without asking for confirmation")
	cmd.Flags().BoolVar(&o.force, "force", false, "Upload even if validation reports errors (requires --note explaining why)")
	return cmd
}

func addSubmitFlags(cmd *cobra.Command, o *submitOptions) {
	fl := cmd.Flags()
	fl.StringVarP(&o.configPath, "config", "c", "", "Path to ctan.toml (default: search the current directory and its parents)")
	fl.StringVar(&o.version, "version", "", "Package version (default: from ctan.toml)")
	fl.StringVar(&o.update, "update", "auto", "Whether the package already exists on CTAN: auto, true or false")
	fl.StringVarP(&o.announcement, "announcement", "a", "", "Announcement text for the CTAN mailing list (\"\" for none)")
	fl.StringVarP(&o.announcementFile, "announcement-file", "A", "", "Read the announcement from a file (\"-\" for standard input)")
	fl.StringVar(&o.note, "note", "", "Private note to the CTAN upload managers")
	fl.StringVar(&o.noteFile, "note-file", "", "Read the note from a file (\"-\" for standard input)")
	fl.StringVar(&o.uploader, "uploader", "", "Name of the uploader (default: $CTANGO_UPLOADER, ctan.toml, user config, git)")
	fl.StringVar(&o.email, "email", "", "Email of the uploader (default: $CTANGO_EMAIL, ctan.toml, user config, git)")
	fl.BoolVarP(&o.dryRun, "dry-run", "n", false, "Show what would be sent without contacting the submission service")
	fl.BoolVar(&o.jsonOut, "json", false, "Print the server response (or, with --dry-run, the fields) as JSON")
	fl.BoolVar(&o.skipChecks, "skip-checks", false, "Skip the local checks of fields and archive")
}

func runSubmit(cmd *cobra.Command, f *Factory, o *submitOptions, args []string) error {
	t := f.IO
	ctx := cmd.Context()

	s, err := o.build(cmd, f, args)
	if err != nil {
		return err
	}

	action, kind := "Validating", "new package"
	if o.upload {
		action = "Uploading"
	}
	if o.dryRun {
		action = "Dry run:"
	}
	if s.Update {
		kind = "update"
	}
	fmt.Fprintf(t.ErrOut, "%s %s %s (%s) from %s\n",
		action, t.Bold(s.Pkg), s.Version, kind, archiveLabel(s.Archive))

	// Local checks.
	if !o.skipChecks {
		msgs := s.Check()
		// A missing or unsupported archive is already reported by Check.
		if st, err := os.Stat(s.Archive); err == nil && !st.IsDir() && submit.HasArchiveExt(s.Archive) {
			if am, err := archive.Inspect(s.Archive, s.Pkg); err != nil {
				msgs = append(msgs, submit.Message{Severity: submit.SeverityError, Text: err.Error()})
			} else {
				msgs = append(msgs, am...)
			}
		}
		msgs = append(msgs, o.lookupMessages...)
		t.PrintMessages(msgs)
		if hasErrors(msgs) {
			t.Failure("Local checks failed (use --skip-checks to send anyway)")
			return errFailed
		}
		t.Success("Local checks passed")
	}

	if o.dryRun {
		return printForm(t.Out, s, o.jsonOut)
	}

	client := f.SubmitClient()
	fmt.Fprintf(t.ErrOut, "Validating with %s ...\n", client.BaseURL)
	res, err := client.Validate(ctx, s)
	if err != nil {
		return err
	}
	t.PrintMessages(res.Messages)
	if !o.upload {
		if o.jsonOut {
			if err := writeRawJSON(t.Out, res.Raw); err != nil {
				return err
			}
		}
		if res.HasErrors() {
			t.Failure("Validation failed")
			return errFailed
		}
		t.Success("Validation passed; the submission can be uploaded with \"ctango upload\"")
		return nil
	}

	if res.HasErrors() {
		if !o.force {
			t.Failure("Validation failed; nothing was uploaded")
			return errFailed
		}
		if strings.TrimSpace(s.Note) == "" {
			t.Failure("Validation failed; --force requires a --note explaining the errors to the CTAN team")
			return errFailed
		}
		fmt.Fprintln(t.ErrOut, "Validation reported errors; continuing because of --force.")
	} else {
		t.Success("Validation passed")
	}

	printSummary(t.ErrOut, s)
	if !o.yes {
		if !t.InteractiveIn {
			return errors.New("refusing to upload without confirmation; pass --yes")
		}
		ok, err := t.Confirm("Upload to CTAN?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(t.ErrOut, "Upload cancelled.")
			return errFailed
		}
	}

	res, err = client.Upload(ctx, s)
	if err != nil {
		var uerr *url.Error
		if !errors.As(err, &uerr) {
			return err
		}
		// The request may have been sent before the connection broke, and
		// CTAN sends no automatic receipt; only the CTAN team can tell.
		t.Failure("No response from CTAN: %v", err)
		fmt.Fprintln(t.ErrOut, "The upload may or may not have reached CTAN. Wait for mail from the CTAN team before uploading again.")
		return errFailed
	}
	t.PrintMessages(res.Messages)
	if o.jsonOut {
		if err := writeRawJSON(t.Out, res.Raw); err != nil {
			return err
		}
	}
	if res.HasErrors() || !uploadSucceeded(res) {
		t.Failure("Upload failed")
		return errFailed
	}
	t.Success("Uploaded %s %s to CTAN; the CTAN team will contact %s", s.Pkg, s.Version, s.Email)
	return nil
}

// uploadSucceeded looks for the explicit confirmation of the server. Old
// servers may not send it, so a clean 200 without "Upload failed" counts too.
func uploadSucceeded(res *submit.Result) bool {
	for _, m := range res.Messages {
		if m.Severity == submit.SeverityInfo && strings.EqualFold(m.Text, "Upload failed") {
			return false
		}
	}
	return true
}

func hasErrors(msgs []submit.Message) bool {
	for _, m := range msgs {
		if m.Severity == submit.SeverityError {
			return true
		}
	}
	return false
}

// build merges flags, environment, ctan.toml, user config and git config
// into a Submission.
func (o *submitOptions) build(cmd *cobra.Command, f *Factory, args []string) (*submit.Submission, error) {
	ctx := cmd.Context()
	flags := cmd.Flags()

	path := o.configPath
	if path == "" {
		var err error
		if path, err = config.Find("."); err != nil {
			return nil, err
		}
	}
	p, err := config.Load(path)
	if err != nil {
		return nil, err
	}

	s := &submit.Submission{
		Pkg:          p.Pkg,
		Authors:      p.Author,
		CTANPath:     p.CTANPath,
		Summary:      strings.TrimSpace(p.Summary),
		Description:  strings.TrimSpace(p.Description),
		Licenses:     p.License,
		Topics:       p.Topic,
		Home:         p.Home,
		Bugtracker:   p.Bugtracker,
		Support:      p.Support,
		Repository:   p.Repository,
		Development:  p.Development,
		Announcement: strings.TrimSpace(p.Announcement),
		Note:         strings.TrimSpace(p.Note),
	}

	// Archive: argument > ctan.toml (relative to its directory).
	if len(args) > 0 {
		s.Archive = args[0]
	} else {
		s.Archive = p.Resolve(p.Archive)
	}

	// Version: flag > ctan.toml (literal or extracted from a file).
	if o.version != "" {
		s.Version = o.version
	} else if s.Version, err = p.ResolveVersion(); err != nil {
		return nil, err
	}

	// Uploader and email: flag > env > ctan.toml > user config > git.
	user, err := config.LoadUser()
	if err != nil {
		return nil, err
	}
	var git config.User
	if first(o.uploader, os.Getenv("CTANGO_UPLOADER"), p.Uploader, user.Name) == "" ||
		first(o.email, os.Getenv("CTANGO_EMAIL"), p.Email, user.Email) == "" {
		git = config.GitUser(p.Dir)
	}
	s.Uploader = first(o.uploader, os.Getenv("CTANGO_UPLOADER"), p.Uploader, user.Name, git.Name)
	s.Email = first(o.email, os.Getenv("CTANGO_EMAIL"), p.Email, user.Email, git.Email)

	// Note.
	if flags.Changed("note") {
		s.Note = strings.TrimSpace(o.note)
	}
	if o.noteFile != "" {
		if s.Note, err = readText(f.IO.In, o.noteFile); err != nil {
			return nil, err
		}
	}

	// Update: explicit or looked up on CTAN.
	switch strings.ToLower(o.update) {
	case "true", "yes":
		s.Update = true
	case "false", "no":
		s.Update = false
	case "auto", "":
		if s.Pkg == "" {
			break
		}
		pkg, _, err := f.APIClient().Package(ctx, s.Pkg)
		switch {
		case errors.Is(err, ctanapi.ErrNotFound):
			s.Update = false
		case err != nil:
			return nil, fmt.Errorf("cannot determine whether %s exists on CTAN (%v); pass --update=true or --update=false", s.Pkg, err)
		default:
			s.Update = true
			o.lookupMessages = compareWithCTAN(s, pkg)
		}
	default:
		return nil, fmt.Errorf("invalid --update value %q (expected auto, true or false)", o.update)
	}

	// Announcement: flag > file > ctan.toml > editor (upload only).
	switch {
	case flags.Changed("announcement"):
		s.Announcement = strings.TrimSpace(o.announcement)
	case o.announcementFile != "":
		if s.Announcement, err = readText(f.IO.In, o.announcementFile); err != nil {
			return nil, err
		}
	case s.Announcement == "" && o.upload && !o.dryRun && f.IO.InteractiveIn:
		tmpl := fmt.Sprintf(`
# Announcement of %s %s for the CTAN mailing list (ctan-ann).
# Lines starting with '#' are ignored. Leave the text empty for no announcement.
`, s.Pkg, s.Version)
		if s.Announcement, err = f.IO.Edit(tmpl); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// compareWithCTAN reports differences to what is currently on CTAN.
func compareWithCTAN(s *submit.Submission, pkg *ctanapi.Package) []submit.Message {
	var msgs []submit.Message
	// The version field may carry a date ("1.8 2026-10-09"); CTAN keeps the
	// number and the date apart.
	if v := pkg.Version.Number; v != "" && v == versionNumber(s.Version) {
		msgs = append(msgs, submit.Message{Severity: submit.SeverityError,
			Text: fmt.Sprintf("version %s is already on CTAN; bump the version", v)})
	}
	if p := pkg.CTANPath(); p != "" && s.CTANPath != "" && p != s.CTANPath {
		msgs = append(msgs, submit.Message{Severity: submit.SeverityWarning,
			Text: fmt.Sprintf("ctanPath %s differs from the current CTAN path %s", s.CTANPath, p)})
	}
	return msgs
}

// versionNumber returns the version without a trailing date.
func versionNumber(v string) string {
	if f := strings.Fields(v); len(f) > 0 {
		return f[0]
	}
	return ""
}

func readText(stdin io.Reader, name string) (string, error) {
	var b []byte
	var err error
	if name == "-" {
		b, err = io.ReadAll(stdin)
	} else {
		b, err = os.ReadFile(name)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func archiveLabel(name string) string {
	if name == "" {
		return "(no archive)"
	}
	st, err := os.Stat(name)
	if err != nil {
		return filepath.Base(name)
	}
	return fmt.Sprintf("%s, %s", filepath.Base(name), humanSize(st.Size()))
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

func printSummary(w io.Writer, s *submit.Submission) {
	fmt.Fprintln(w)
	row := func(k, v string) { fmt.Fprintf(w, "  %-13s %s\n", k+":", v) }
	row("Package", s.Pkg)
	row("Version", s.Version)
	row("Update", fmt.Sprint(s.Update))
	row("CTAN path", s.CTANPath)
	row("Archive", archiveLabel(s.Archive))
	row("Uploader", fmt.Sprintf("%s <%s>", s.Uploader, s.Email))
	row("Announcement", preview(s.Announcement, "(none; no mail will be sent)"))
	if s.Note != "" {
		row("Note", preview(s.Note, ""))
	}
	fmt.Fprintln(w)
}

func preview(text, empty string) string {
	if text == "" {
		return empty
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 3 {
		lines = append(lines[:3], fmt.Sprintf("... (%d more lines)", len(lines)-3))
	}
	return strings.Join(lines, "\n"+strings.Repeat(" ", 16))
}

// printForm shows the multipart fields that would be sent.
func printForm(w io.Writer, s *submit.Submission, asJSON bool) error {
	fields := s.Fields()
	if asJSON {
		m := map[string]any{}
		for _, fl := range fields {
			switch prev := m[fl.Name].(type) {
			case nil:
				m[fl.Name] = fl.Value
			case string:
				m[fl.Name] = []string{prev, fl.Value}
			case []string:
				m[fl.Name] = append(prev, fl.Value)
			}
		}
		m["file"] = s.Archive
		return writeJSON(w, m)
	}
	for _, fl := range fields {
		v := strings.ReplaceAll(fl.Value, "\n", "\n"+strings.Repeat(" ", 15))
		fmt.Fprintf(w, "%-14s %s\n", fl.Name+":", v)
	}
	fmt.Fprintf(w, "%-14s %s\n", "file:", archiveLabel(s.Archive))
	return nil
}
