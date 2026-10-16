// Package config reads the per-package ctan.toml and the per-user
// configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the name of the per-package configuration file.
const FileName = "ctan.toml"

// StringList accepts both `key = "x"` and `key = ["x", "y"]`.
type StringList []string

func (l *StringList) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case string:
		*l = StringList{v}
	case []any:
		out := make(StringList, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("expected a string, got %T", e)
			}
			out = append(out, s)
		}
		*l = out
	default:
		return fmt.Errorf("expected a string or an array of strings, got %T", v)
	}
	return nil
}

// VersionSpec is either a literal version (`version = "1.8"`) or a rule to
// extract it from a source file:
//
//	[version]
//	file    = "foo.sty"
//	pattern = '...'   # optional; first capture group is the version
type VersionSpec struct {
	Value   string
	File    string
	Pattern string
}

func (s *VersionSpec) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case string:
		s.Value = v
	case map[string]any:
		for k, e := range v {
			str, ok := e.(string)
			if !ok {
				return fmt.Errorf("version.%s: expected a string, got %T", k, e)
			}
			switch k {
			case "file":
				s.File = str
			case "pattern":
				s.Pattern = str
			default:
				return fmt.Errorf("version: unknown key %q (expected file or pattern)", k)
			}
		}
		if s.File == "" {
			return errors.New("version: file is required")
		}
	default:
		return fmt.Errorf("version: expected a string or a table, got %T", v)
	}
	return nil
}

// Project is the content of ctan.toml. Keys are named after the fields of
// the CTAN submission API.
type Project struct {
	Pkg          string      `toml:"pkg"`
	Version      VersionSpec `toml:"version"`
	Author       StringList  `toml:"author"`
	Uploader     string      `toml:"uploader"`
	Email        string      `toml:"email"`
	CTANPath     string      `toml:"ctanPath"`
	Summary      string      `toml:"summary"`
	Description  string      `toml:"description"`
	License      StringList  `toml:"license"`
	Topic        StringList  `toml:"topic"`
	Home         StringList  `toml:"home"`
	Bugtracker   StringList  `toml:"bugtracker"`
	Support      StringList  `toml:"support"`
	Repository   StringList  `toml:"repository"`
	Development  StringList  `toml:"development"`
	Announcement string      `toml:"announcement"`
	Note         string      `toml:"note"`
	Archive      string      `toml:"archive"`

	// Path is the file the project was loaded from; Dir its directory.
	Path string `toml:"-"`
	Dir  string `toml:"-"`
}

// Find looks for ctan.toml in dir and its parents.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, FileName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s not found (run `ctango init` to create one)", FileName)
		}
		dir = parent
	}
}

// Load reads a ctan.toml. Unknown keys are reported as errors so that typos
// such as "ctanpath" do not silently drop a value.
func Load(path string) (*Project, error) {
	var p Project
	md, err := toml.DecodeFile(path, &p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p.Path = abs
	p.Dir = filepath.Dir(abs)
	return &p, nil
}

// Resolve interprets a path from ctan.toml relative to its directory.
func (p *Project) Resolve(name string) string {
	if name == "" || filepath.IsAbs(name) || p.Dir == "" {
		return name
	}
	return filepath.Join(p.Dir, name)
}

// ResolveVersion returns the literal version, or the version (with its
// date, as CTAN asks for) extracted from a file.
func (p *Project) ResolveVersion() (string, error) {
	if p.Version.Value != "" {
		return p.Version.Value, nil
	}
	if p.Version.File == "" {
		return "", nil
	}
	v, err := ExtractVersion(p.Resolve(p.Version.File), p.Version.Pattern)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// Version is a version number with an optional release date.
type Version struct {
	Number string
	Date   string // YYYY-MM-DD, or empty
}

// String returns the value for the version field of an upload, e.g.
// "1.8 2026-10-09". The CTAN team asks for the date to be included.
func (v Version) String() string {
	if v.Date == "" {
		return v.Number
	}
	return v.Number + " " + v.Date
}

// DefaultVersionPatterns match the usual LaTeX identification commands.
// The named groups "version" and "date" pick up the two parts.
var DefaultVersionPatterns = []string{
	// \ProvidesExplPackage {name} {2026-10-09} {1.8} {description}
	`\\ProvidesExpl(?:Package|Class|File)\s*\{[^}]*\}\s*\{\s*(?P<date>[^}\s]*)\s*\}\s*\{\s*v?(?P<version>[^}\s]+)\s*\}`,
	// \ProvidesPackage{name}[2026/10/09 v1.8 description]
	`\\Provides(?:Package|Class|File)\s*\{[^}]*\}\s*\[\s*(?P<date>\S+)\s+v?(?P<version>[^\s\]]+)`,
}

var dateRe = regexp.MustCompile(`^(\d{4})[-/](\d{2})[-/](\d{2})$`)

// normalizeDate turns 2026/10/09 into 2026-10-09 and drops anything that
// is not a date.
func normalizeDate(s string) string {
	m := dateRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return ""
	}
	return m[1] + "-" + m[2] + "-" + m[3]
}

// ExtractVersion finds pattern in file. The version is taken from the
// named group "version", else the first group, else the whole match; the
// date from the named group "date", if any. With an empty pattern,
// DefaultVersionPatterns are tried.
func ExtractVersion(file, pattern string) (Version, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return Version{}, fmt.Errorf("version: %w", err)
	}
	patterns := DefaultVersionPatterns
	if pattern != "" {
		patterns = []string{pattern}
	}
	for _, pat := range patterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			return Version{}, fmt.Errorf("version.pattern: %w", err)
		}
		m := re.FindSubmatch(b)
		if m == nil {
			continue
		}
		var v Version
		number := m[0]
		if i := re.SubexpIndex("version"); i >= 0 {
			number = m[i]
		} else if len(m) > 1 {
			number = m[1]
		}
		v.Number = strings.TrimSpace(string(number))
		if i := re.SubexpIndex("date"); i >= 0 {
			v.Date = normalizeDate(string(m[i]))
		}
		if v.Number != "" {
			return v, nil
		}
	}
	return Version{}, fmt.Errorf("version: no match in %s", file)
}

// User is the per-user configuration, ~/.config/ctango/config.toml:
//
//	[user]
//	name  = "Jane Doe"
//	email = "jane@example.org"
type User struct {
	Name  string `toml:"name"`
	Email string `toml:"email"`
}

type userFile struct {
	User User `toml:"user"`
}

// UserConfigPath returns $CTANGO_CONFIG, $XDG_CONFIG_HOME/ctango/config.toml or
// ~/.config/ctango/config.toml.
func UserConfigPath() string {
	if p := os.Getenv("CTANGO_CONFIG"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "ctango", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ctango", "config.toml")
}

// LoadUser reads the per-user configuration. A missing file is not an error.
func LoadUser() (User, error) {
	path := UserConfigPath()
	if path == "" {
		return User{}, nil
	}
	var f userFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return User{}, nil
		}
		return User{}, fmt.Errorf("%s: %w", path, err)
	}
	return f.User, nil
}

// GitUser returns user.name and user.email from git config, as seen from dir.
func GitUser(dir string) User {
	get := func(key string) string {
		cmd := exec.Command("git", "config", "--get", key)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return User{Name: get("user.name"), Email: get("user.email")}
}
