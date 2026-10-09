// Package submit talks to the CTAN submission API described at
// https://ctan.org/help/submit.
package submit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Submission holds everything that is sent to /submit/validate or
// /submit/upload. Field names follow the API, not Go conventions, in the
// comments so that they can be matched against the documentation.
type Submission struct {
	Pkg          string   // pkg
	Version      string   // version
	Authors      []string // author (joined with "; ")
	Uploader     string   // uploader
	Email        string   // email
	CTANPath     string   // ctanPath
	Summary      string   // summary
	Description  string   // description
	Update       bool     // update
	Announcement string   // announcement
	Note         string   // note
	Licenses     []string // license (repeated)
	Topics       []string // topic (repeated)
	Home         []string // home (repeated)
	Bugtracker   []string // bugtracker (repeated)
	Support      []string // support (repeated)
	Repository   []string // repository (repeated)
	Development  []string // development (repeated)
	Archive      string   // file
}

// Field is a single text part of the multipart request.
type Field struct {
	Name  string
	Value string
}

// Fields returns the text parts in a stable order. Empty optional values
// are omitted; mandatory ones are always present so that the server can
// complain about them.
func (s *Submission) Fields() []Field {
	var f []Field
	add := func(name, value string) { f = append(f, Field{name, value}) }
	addOpt := func(name, value string) {
		if value != "" {
			add(name, value)
		}
	}
	addList := func(name string, values []string) {
		for _, v := range values {
			addOpt(name, v)
		}
	}

	add("pkg", s.Pkg)
	add("version", s.Version)
	// The API documents author as a field that may appear at most once;
	// several authors are separated by semicolons.
	add("author", strings.Join(s.Authors, "; "))
	add("uploader", s.Uploader)
	add("email", s.Email)
	add("ctanPath", s.CTANPath)
	add("summary", s.Summary)
	add("description", s.Description)
	add("update", strconv.FormatBool(s.Update))
	addList("license", s.Licenses)
	addList("topic", s.Topics)
	addList("home", s.Home)
	addList("bugtracker", s.Bugtracker)
	addList("support", s.Support)
	addList("repository", s.Repository)
	addList("development", s.Development)
	addOpt("announcement", s.Announcement)
	addOpt("note", s.Note)
	return f
}

// Maximum field lengths from the API documentation (and /submit/fields).
var maxLength = map[string]int{
	"announcement": 8192,
	"author":       128,
	"bugtracker":   255,
	"ctanPath":     255,
	"description":  4096,
	"development":  255,
	"email":        255,
	"home":         255,
	"license":      2048,
	"note":         2048,
	"pkg":          32,
	"repository":   255,
	"summary":      128,
	"support":      255,
	"topic":        1024,
	"update":       8,
	"uploader":     255,
	"version":      32,
}

var mandatory = []string{
	"pkg", "version", "author", "uploader", "email",
	"ctanPath", "summary", "description", "update",
}

var urlFields = map[string]bool{
	"home": true, "bugtracker": true, "support": true,
	"repository": true, "development": true,
}

var (
	pkgRe   = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	urlRe   = regexp.MustCompile(`^(https?://|ftp://|mailto:)`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// Check performs the validations that can be done without the server, so
// that obvious mistakes are reported before anything is sent.
func (s *Submission) Check() []Message {
	var msgs []Message
	errorf := func(format string, a ...any) {
		msgs = append(msgs, Message{Severity: SeverityError, Text: fmt.Sprintf(format, a...)})
	}
	warnf := func(format string, a ...any) {
		msgs = append(msgs, Message{Severity: SeverityWarning, Text: fmt.Sprintf(format, a...)})
	}

	fields := s.Fields()
	present := map[string]bool{}
	for _, f := range fields {
		if strings.TrimSpace(f.Value) != "" {
			present[f.Name] = true
		}
		if max, ok := maxLength[f.Name]; ok {
			if n := utf8.RuneCountInString(f.Value); n > max {
				errorf("field %s is too long (%d > %d characters)", f.Name, n, max)
			}
		}
		if urlFields[f.Name] && !urlRe.MatchString(f.Value) {
			errorf("field %s must start with http://, https://, ftp:// or mailto: (%q)", f.Name, f.Value)
		}
	}
	for _, name := range mandatory {
		if !present[name] {
			errorf("missing mandatory field %s", name)
		}
	}

	if s.Pkg != "" && !pkgRe.MatchString(s.Pkg) {
		if pkgRe.MatchString(strings.ToLower(s.Pkg)) {
			warnf("package name %q will be lowercased by CTAN", s.Pkg)
		} else {
			errorf("illegal package name %q (lowercase letters, digits, - and _; must start with a letter)", s.Pkg)
		}
	}
	if s.Email != "" && !emailRe.MatchString(s.Email) {
		errorf("email %q does not look like an email address", s.Email)
	}
	if s.CTANPath != "" && !strings.HasPrefix(s.CTANPath, "/") {
		errorf("ctanPath %q must start with / (path below /tex-archive)", s.CTANPath)
	}
	if strings.ContainsAny(s.Summary, "\r\n") {
		errorf("summary must be a single line")
	}

	switch {
	case s.Archive == "":
		errorf("no archive file given")
	default:
		if !HasArchiveExt(s.Archive) {
			errorf("archive %s must be a .zip, .tgz or .tar.gz file", filepath.Base(s.Archive))
		}
		if st, err := os.Stat(s.Archive); err != nil {
			errorf("archive: %v", err)
		} else if st.IsDir() {
			errorf("archive %s is a directory", s.Archive)
		}
	}
	return msgs
}

// HasArchiveExt reports whether name has an extension accepted by CTAN.
func HasArchiveExt(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".zip") || strings.HasSuffix(n, ".tgz") || strings.HasSuffix(n, ".tar.gz")
}
