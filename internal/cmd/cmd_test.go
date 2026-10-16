package cmd

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/munepi/ctango/internal/config"
	"github.com/munepi/ctango/internal/ui"
)

// fakeCTAN serves the submission API and the JSON API.
type fakeCTAN struct {
	mu        sync.Mutex
	calls     []string
	forms     map[string]map[string][]string
	validate  string // response body of /submit/validate
	ctanPkgV  string // version of "foo" on CTAN; "" = not found
	uploadErr bool
	// uploadDrop closes the connection without answering /submit/upload.
	uploadDrop bool
}

func (c *fakeCTAN) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, r.Method+" "+r.URL.Path)
	switch r.URL.Path {
	case "/submit/validate", "/submit/upload":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if c.forms == nil {
			c.forms = map[string]map[string][]string{}
		}
		c.forms[r.URL.Path] = r.MultipartForm.Value
		if r.URL.Path == "/submit/validate" {
			if strings.Contains(c.validate, "ERROR") {
				w.WriteHeader(http.StatusConflict)
			}
			w.Write([]byte(c.validate))
			return
		}
		if c.uploadDrop {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		if c.uploadErr {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`[["ERROR","Version already exists","1.0"],["INFO","Upload failed"]]`))
			return
		}
		w.Write([]byte(`[["INFO","Upload succeeded"]]`))
	case "/json/pkg/foo":
		if c.ctanPkgV == "" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"errors":["Not found"]}`))
			return
		}
		w.Write([]byte(`{"id":"foo","caption":"The foo package","authors":[{"id":"doe","active":true}],
			"license":["lppl1.3c","mit"],"version":{"number":"` + c.ctanPkgV + `","date":"2026-01-01"},
			"descriptions":[{"language":null,"text":"<p>\n   Uses <ref refid=\"xcolor\">xcolor</ref>.\n  </p>"}],
			"bugs":"https://example.org/issues","ctan":{"path":"/macros/latex/contrib/foo","file":true},
			"topics":["colour"]}`))
	case "/json/author/doe":
		w.Write([]byte(`{"key":"doe","givenname":"Jane","von":"","familyname":"Doe","junior":""}`))
	default:
		http.NotFound(w, r)
	}
}

type testEnv struct {
	dir    string
	ctan   *fakeCTAN
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	fake := &fakeCTAN{validate: `[["INFO","ok"]]`}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	t.Setenv("CTANGO_SUBMIT_URL", srv.URL+"/submit")
	t.Setenv("CTANGO_API_URL", srv.URL+"/json")
	t.Setenv("CTANGO_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	t.Setenv("CTANGO_UPLOADER", "Jane Doe")
	t.Setenv("CTANGO_EMAIL", "jane@example.org")

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "foo.sty"), "\\ProvidesExplPackage {foo} {2026-10-09} {1.1} {Foo}\n")
	mustWrite(t, filepath.Join(dir, config.FileName), `
pkg = "foo"
ctanPath = "/macros/latex/contrib/foo"
summary = "The foo package"
description = "<p>Foo.</p>"
author = ["Jane Doe"]
license = ["lppl1.3c"]
topic = ["colour"]
archive = "foo.zip"

[version]
file = "foo.sty"
`)
	z, err := os.Create(filepath.Join(dir, "foo.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(z)
	for _, n := range []string{"foo/README.md", "foo/foo.sty", "foo/foo.pdf"} {
		w, _ := zw.Create(n)
		w.Write([]byte("x"))
	}
	zw.Close()
	z.Close()

	return &testEnv{dir: dir, ctan: fake, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
}

func (e *testEnv) run(t *testing.T, stdin string, args ...string) int {
	t.Helper()
	e.stdout.Reset()
	e.stderr.Reset()
	f := &Factory{
		IO:      &ui.IO{In: strings.NewReader(stdin), Out: e.stdout, ErrOut: e.stderr},
		Version: "test",
	}
	wd, _ := os.Getwd()
	if err := os.Chdir(e.dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	return Execute(f, args)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUploadNewPackage(t *testing.T) {
	e := setup(t)
	if code := e.run(t, "", "upload", "--yes", "-a", "Hello CTAN"); code != 0 {
		t.Fatalf("exit %d\n%s", code, e.stderr)
	}
	want := []string{"GET /json/pkg/foo", "POST /submit/validate", "POST /submit/upload"}
	if strings.Join(e.ctan.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v", e.ctan.calls)
	}
	form := e.ctan.forms["/submit/upload"]
	checks := map[string]string{
		"pkg": "foo", "version": "1.1 2026-10-09", "update": "false", "uploader": "Jane Doe",
		"email": "jane@example.org", "announcement": "Hello CTAN", "author": "Jane Doe",
	}
	for k, v := range checks {
		if got := form[k]; len(got) != 1 || got[0] != v {
			t.Errorf("%s = %v, want %q", k, got, v)
		}
	}
	if !strings.Contains(e.stderr.String(), "Uploaded foo 1.1") {
		t.Errorf("stderr:\n%s", e.stderr)
	}
}

func TestUploadUpdateAndConfirm(t *testing.T) {
	e := setup(t)
	e.ctan.ctanPkgV = "1.0"

	// Non-interactive without --yes refuses.
	if code := e.run(t, "", "upload", "-a", ""); code == 0 {
		t.Fatal("expected refusal without --yes")
	}
	if strings.Contains(strings.Join(e.ctan.calls, ","), "/submit/upload") {
		t.Fatal("uploaded without confirmation")
	}

	// Interactive "n" cancels, "y" uploads.
	f := func(answer string) int {
		e.stdout.Reset()
		e.stderr.Reset()
		fac := &Factory{IO: &ui.IO{In: strings.NewReader(answer), Out: e.stdout, ErrOut: e.stderr, InteractiveIn: true}, Version: "test"}
		wd, _ := os.Getwd()
		os.Chdir(e.dir)
		defer os.Chdir(wd)
		return Execute(fac, []string{"upload", "-a", ""})
	}
	if code := f("n\n"); code == 0 {
		t.Error("expected cancel")
	}
	if code := f("y\n"); code != 0 {
		t.Fatalf("exit %d\n%s", code, e.stderr)
	}
	if got := e.ctan.forms["/submit/upload"]["update"]; got[0] != "true" {
		t.Errorf("update = %v", got)
	}
	if _, ok := e.ctan.forms["/submit/upload"]["announcement"]; ok {
		t.Error("empty announcement should not be sent")
	}
}

func TestSameVersionOnCTAN(t *testing.T) {
	e := setup(t)
	e.ctan.ctanPkgV = "1.1"
	if code := e.run(t, "", "validate"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(e.stderr.String(), "already on CTAN") {
		t.Errorf("stderr:\n%s", e.stderr)
	}
	for _, c := range e.ctan.calls {
		if strings.HasPrefix(c, "POST") {
			t.Errorf("unexpected call %s", c)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	e := setup(t)
	e.ctan.validate = `[["ERROR","Missing PDF documentation"]]`
	if code := e.run(t, "", "upload", "--yes", "-a", ""); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(strings.Join(e.ctan.calls, ","), "/submit/upload") {
		t.Fatal("uploaded despite validation errors")
	}
	// --force needs a note.
	if code := e.run(t, "", "upload", "--yes", "-a", "", "--force"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if code := e.run(t, "", "upload", "--yes", "-a", "", "--force", "--note", "The PDF is built by CTAN."); code != 0 {
		t.Fatalf("exit %d\n%s", code, e.stderr)
	}
}

func TestUploadFailed(t *testing.T) {
	e := setup(t)
	e.ctan.uploadErr = true
	if code := e.run(t, "", "upload", "--yes", "-a", ""); code != 1 {
		t.Fatalf("exit %d", code)
	}
}

func TestDryRun(t *testing.T) {
	e := setup(t)
	if code := e.run(t, "", "upload", "--dry-run", "--version", "9.9", "--update", "true", "--note-file", "-"); code != 0 {
		t.Fatalf("exit %d\n%s", code, e.stderr)
	}
	out := e.stdout.String()
	for _, want := range []string{"version:       9.9", "update:        true", "file:          foo.zip"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if len(e.ctan.calls) != 0 {
		t.Errorf("calls = %v", e.ctan.calls)
	}
}

func TestInit(t *testing.T) {
	e := setup(t)
	e.ctan.ctanPkgV = "1.0"
	os.Remove(filepath.Join(e.dir, config.FileName))
	if code := e.run(t, "", "init", "foo"); code != 0 {
		t.Fatalf("exit %d\n%s", code, e.stderr)
	}
	p, err := config.Load(filepath.Join(e.dir, config.FileName))
	if err != nil {
		b, _ := os.ReadFile(filepath.Join(e.dir, config.FileName))
		t.Fatalf("%v\n%s", err, b)
	}
	if p.Summary != "The foo package" || p.CTANPath != "/macros/latex/contrib/foo" ||
		strings.Join(p.Author, ",") != "Jane Doe" || len(p.License) != 2 ||
		p.Bugtracker[0] != "https://example.org/issues" || p.Archive != "foo.zip" ||
		p.Version.File != "foo.sty" {
		t.Errorf("project = %+v", p)
	}
	if strings.TrimSpace(p.Description) != "<p>\nUses xcolor.\n</p>" {
		t.Errorf("description = %q", p.Description)
	}
	// Refuses to overwrite.
	if code := e.run(t, "", "init", "foo"); code == 0 {
		t.Error("expected refusal to overwrite")
	}
}

func TestUploadNoResponse(t *testing.T) {
	e := setup(t)
	e.ctan.uploadDrop = true
	if code := e.run(t, "", "upload", "--yes", "-a", ""); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(e.stderr.String(), "may or may not have reached CTAN") {
		t.Errorf("stderr:\n%s", e.stderr)
	}
}

func TestMissingArchiveReportedOnce(t *testing.T) {
	e := setup(t)
	if code := e.run(t, "", "validate", "--update", "true", "missing.zip"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if n := strings.Count(e.stderr.String(), "ERROR"); n != 1 {
		t.Errorf("%d errors reported, want 1:\n%s", n, e.stderr)
	}
}
