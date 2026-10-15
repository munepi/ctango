package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/munepi/ctango/internal/config"
	"github.com/munepi/ctango/internal/ctanapi"
)

type initOptions struct {
	output  string
	force   bool
	noFetch bool
}

func newInitCmd(f *Factory) *cobra.Command {
	o := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init [<pkg>]",
		Short: "Create ctan.toml for a package",
		Long: `Create ctan.toml for a package.

If the package already exists on CTAN, its metadata (summary,
description, authors, license, topics, CTAN path and URLs) is
filled in from the CTAN JSON API. The package name defaults to the
name of the current directory.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runInit(cmd, f, o, args) },
	}
	cmd.Flags().StringVarP(&o.output, "output", "o", config.FileName, "File to write")
	cmd.Flags().BoolVar(&o.force, "force", false, "Overwrite an existing file")
	cmd.Flags().BoolVar(&o.noFetch, "no-fetch", false, "Do not look the package up on CTAN")
	return cmd
}

func runInit(cmd *cobra.Command, f *Factory, o *initOptions, args []string) error {
	ctx := cmd.Context()
	t := f.IO

	var name string
	if len(args) > 0 {
		name = args[0]
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		name = strings.ToLower(filepath.Base(wd))
	}

	if !o.force {
		if _, err := os.Stat(o.output); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", o.output)
		}
	}

	var pkg *ctanapi.Package
	var authors []string
	if !o.noFetch {
		api := f.APIClient()
		var err error
		pkg, _, err = api.Package(ctx, name)
		switch {
		case errors.Is(err, ctanapi.ErrNotFound):
			fmt.Fprintf(t.ErrOut, "%s is not on CTAN yet; writing a template for a new package.\n", name)
			pkg = nil
		case err != nil:
			return fmt.Errorf("looking up %s on CTAN: %w (use --no-fetch to skip)", name, err)
		default:
			fmt.Fprintf(t.ErrOut, "Found %s %s on CTAN; using its metadata.\n", pkg.ID, pkg.Version.Number)
			for _, a := range pkg.Authors {
				if au, err := api.Author(ctx, a.ID); err == nil {
					authors = append(authors, au.Name())
				} else {
					authors = append(authors, a.ID)
				}
			}
		}
	}

	dir := filepath.Dir(o.output)
	content := renderInit(name, pkg, authors, findVersionFile(dir, name), archiveGuess(dir, name))
	if err := os.WriteFile(o.output, []byte(content), 0o644); err != nil {
		return err
	}
	t.Success("Wrote %s; review it, then run \"ctango validate\"", o.output)
	return nil
}

// findVersionFile returns the first of <pkg>.sty, .cls, .dtx, .tex in dir
// from which a version can be extracted with the default patterns.
func findVersionFile(dir, name string) string {
	for _, ext := range []string{".sty", ".cls", ".dtx", ".tex"} {
		file := name + ext
		if _, err := config.ExtractVersion(filepath.Join(dir, file), ""); err == nil {
			return file
		}
	}
	return ""
}

func archiveGuess(dir, name string) string {
	for _, ext := range []string{".zip", ".tar.gz", ".tgz"} {
		if _, err := os.Stat(filepath.Join(dir, name+ext)); err == nil {
			return name + ext
		}
	}
	return name + ".zip"
}

func renderInit(name string, pkg *ctanapi.Package, authors []string, versionFile, archive string) string {
	var b strings.Builder
	kv := func(key, value, comment string) {
		line := fmt.Sprintf("%-11s = %s", key, tomlString(value))
		if comment != "" {
			line += "  # " + comment
		}
		b.WriteString(line + "\n")
	}
	kvList := func(key string, values []string, placeholder string) {
		if len(values) == 0 {
			fmt.Fprintf(&b, "# %-11s = %s\n", key, placeholder)
			return
		}
		quoted := make([]string, len(values))
		for i, v := range values {
			quoted[i] = tomlString(v)
		}
		fmt.Fprintf(&b, "%-11s = [%s]\n", key, strings.Join(quoted, ", "))
	}

	b.WriteString("# ctan.toml -- metadata for \"ctango upload\".\n")
	b.WriteString("# Keys follow the CTAN submission API: https://ctan.org/help/submit\n")
	b.WriteString("# uploader/email are taken from ~/.config/ctango/config.toml or git config.\n\n")

	var summary, description, path string
	var licenses, topics, home, bugs, support, repo, devel []string
	if pkg != nil {
		summary = pkg.Caption
		description = cleanDescription(pkg.Description())
		path = pkg.CTANPath()
		licenses, topics = pkg.License, pkg.Topics
		home, bugs, support = pkg.Home, pkg.Bugs, pkg.Support
		repo, devel = pkg.Repository, pkg.Development
	}
	if path == "" {
		path = "/macros/latex/contrib/" + name
	}

	kv("pkg", name, "")
	pathComment, summaryComment := "", ""
	if pkg == nil {
		pathComment = "where the package should go on CTAN"
	}
	if summary == "" {
		summaryComment = "one line, at most 128 characters"
	}
	kv("ctanPath", path, pathComment)
	kv("summary", summary, summaryComment)
	if description == "" {
		b.WriteString("description = '''\n<p>Describe the package here (HTML allowed, at most 4096 characters).</p>\n'''\n")
	} else {
		fmt.Fprintf(&b, "description = %s\n", tomlMultiline(description))
	}
	kvList("author", authors, `["Your Name"]`)
	kvList("license", licenses, `["lppl1.3c"]  # see https://ctan.org/license`)
	kvList("topic", topics, `["macro-supp"]  # see https://ctan.org/topics/cloud`)
	kvList("home", home, `"https://..."`)
	kvList("bugtracker", bugs, `"https://github.com/you/`+name+`/issues"`)
	kvList("support", support, `"https://..."`)
	kvList("repository", repo, `"https://github.com/you/`+name+`"`)
	kvList("development", devel, `"https://..."`)
	b.WriteString("\n")
	kv("archive", archive, "built by your release process")
	b.WriteString("\n")
	if versionFile != "" {
		b.WriteString("# The version is read from this file (\\ProvidesPackage / \\ProvidesExplPackage).\n")
		b.WriteString("# Add pattern = '...' to use your own regular expression (first group = version).\n")
		b.WriteString("[version]\n")
		kv("file", versionFile, "")
	} else {
		b.WriteString("# Either a literal version, or a [version] table with file (and pattern).\n")
		b.WriteString("# Usually given with --version at upload time.\n")
		b.WriteString("# version = \"1.0\"\n")
	}
	return b.String()
}

var (
	refTagRe   = regexp.MustCompile(`</?(?:ref|xref)\b[^>]*>`)
	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

// cleanDescription turns the description stored on CTAN back into
// submission HTML: CTAN-internal <ref> links are reduced to their text and
// the indentation is removed.
func cleanDescription(s string) string {
	s = refTagRe.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	return strings.TrimSpace(blankRunRe.ReplaceAllString(s, "\n\n"))
}

// tomlString quotes s as a TOML basic string (JSON escaping is compatible).
func tomlString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

// tomlMultiline uses a multi-line literal string when possible.
func tomlMultiline(s string) string {
	if strings.Contains(s, "'''") {
		return tomlString(s)
	}
	return "'''\n" + s + "\n'''"
}
