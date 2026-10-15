package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAndVersion(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "foo.sty", "\\ProvidesExplPackage {foo} {2026-10-09} {1.8} {Foo}\n")
	path := write(t, dir, FileName, `
pkg = "foo"
author = "Jane Doe"
license = ["lppl1.3c", "mit"]
home = "https://example.org"
archive = "dist/foo.zip"

[version]
file = "foo.sty"
`)
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Author, StringList{"Jane Doe"}) || len(p.License) != 2 || p.Home[0] != "https://example.org" {
		t.Errorf("lists: %+v", p)
	}
	if got := p.Resolve(p.Archive); got != filepath.Join(dir, "dist", "foo.zip") {
		t.Errorf("Resolve = %s", got)
	}
	v, err := p.ResolveVersion()
	if err != nil || v != "1.8" {
		t.Errorf("ResolveVersion = %q, %v", v, err)
	}

	// Literal version.
	path = write(t, dir, "b/"+FileName, `version = "2.0"`)
	p, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := p.ResolveVersion(); v != "2.0" {
		t.Errorf("literal version = %q", v)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	path := write(t, t.TempDir(), FileName, "pkg = \"foo\"\nctan_path = \"/x\"\n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "ctan_path") {
		t.Errorf("err = %v", err)
	}
	path = write(t, t.TempDir(), FileName, "[version]\nfile = \"x\"\nregex = \"y\"\n")
	if _, err := Load(path); err == nil {
		t.Error("expected error for unknown key in [version]")
	}
}

func TestExtractVersion(t *testing.T) {
	dir := t.TempDir()
	tests := []struct{ content, pattern, want string }{
		{"\\ProvidesExplPackage {spotxcolor} {2026-10-09} {1.8} {x}", "", "1.8"},
		{"\\ProvidesPackage{foo}\n  [2026/10/09 v2.3a Foo]", "", "2.3a"},
		{"\\ProvidesClass{foo}[2026/10/09 3.1 Foo]", "", "3.1"},
		{"\\def\\fooversion{4.5}", `\\def\\fooversion\{([^}]*)\}`, "4.5"},
	}
	for i, tt := range tests {
		f := write(t, dir, "f.sty", tt.content)
		got, err := ExtractVersion(f, tt.pattern)
		if err != nil || got != tt.want {
			t.Errorf("%d: got %q, %v; want %q", i, got, err, tt.want)
		}
	}
	f := write(t, dir, "g.sty", "nothing")
	if _, err := ExtractVersion(f, ""); err == nil {
		t.Error("expected no match")
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, FileName, "")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(sub)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(filepath.Join(dir, FileName)); mustEval(t, got) != want {
		t.Errorf("Find = %s, want %s", got, want)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLoadUser(t *testing.T) {
	path := write(t, t.TempDir(), "config.toml", "[user]\nname = \"Jane\"\nemail = \"jane@example.org\"\n")
	t.Setenv("CTANGO_CONFIG", path)
	u, err := LoadUser()
	if err != nil || u.Name != "Jane" || u.Email != "jane@example.org" {
		t.Errorf("LoadUser = %+v, %v", u, err)
	}
	t.Setenv("CTANGO_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	if u, err := LoadUser(); err != nil || u != (User{}) {
		t.Errorf("missing file: %+v, %v", u, err)
	}
}
