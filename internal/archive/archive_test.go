package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munepi/ctango/internal/submit"
)

func makeZip(t *testing.T, names ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "foo.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, n := range names {
		fw, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(n, "/") {
			fw.Write([]byte("x"))
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

func makeTarGz(t *testing.T, names ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "foo.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		h := &tar.Header{Name: n, Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}
		if strings.HasSuffix(n, "/") {
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte("x"))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func texts(msgs []submit.Message, severity string) string {
	var out []string
	for _, m := range msgs {
		if m.Severity == severity {
			out = append(out, m.Text)
		}
	}
	return strings.Join(out, "\n")
}

func TestInspectGood(t *testing.T) {
	for _, p := range []string{
		makeZip(t, "foo/", "foo/README.md", "foo/foo.sty", "foo/foo.pdf"),
		makeTarGz(t, "foo/", "foo/README", "foo/foo.sty", "foo/doc/foo.pdf"),
	} {
		msgs, err := Inspect(p, "foo")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 0 {
			t.Errorf("%s: unexpected messages %v", filepath.Base(p), msgs)
		}
	}
}

func TestInspectProblems(t *testing.T) {
	p := makeZip(t, "bar/foo.sty", "bar/doc/README", "bar/foo.aux", "bar/.git/config", "bar/x.tex~")
	msgs, err := Inspect(p, "foo")
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := texts(msgs, submit.SeverityError), texts(msgs, submit.SeverityWarning)
	for _, want := range []string{"missing README", "bar/foo.aux", "bar/x.tex~"} {
		if !strings.Contains(errs, want) {
			t.Errorf("errors lack %q:\n%s", want, errs)
		}
	}
	for _, want := range []string{"expected foo/", "no PDF", "bar/.git/config"} {
		if !strings.Contains(warns, want) {
			t.Errorf("warnings lack %q:\n%s", want, warns)
		}
	}

	p = makeZip(t, "README", "foo.sty", "foo.pdf")
	msgs, _ = Inspect(p, "foo")
	if w := texts(msgs, submit.SeverityWarning); !strings.Contains(w, "top level") {
		t.Errorf("loose files not reported: %v", msgs)
	}
}
