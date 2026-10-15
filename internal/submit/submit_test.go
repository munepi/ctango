package submit

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func sample(t *testing.T) *Submission {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "foo.zip")
	if err := os.WriteFile(archive, []byte("PK fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Submission{
		Pkg:         "foo",
		Version:     "1.0",
		Authors:     []string{"A. Author", "B. Author"},
		Uploader:    "A. Author",
		Email:       "a@example.org",
		CTANPath:    "/macros/latex/contrib/foo",
		Summary:     "A foo package",
		Description: "<p>Foo.</p>",
		Update:      true,
		Licenses:    []string{"lppl1.3c", "mit"},
		Topics:      []string{"colour"},
		Bugtracker:  []string{"https://example.org/issues"},
		Archive:     archive,
	}
}

func TestFields(t *testing.T) {
	s := sample(t)
	got := s.Fields()
	want := []Field{
		{"pkg", "foo"}, {"version", "1.0"}, {"author", "A. Author; B. Author"},
		{"uploader", "A. Author"}, {"email", "a@example.org"},
		{"ctanPath", "/macros/latex/contrib/foo"}, {"summary", "A foo package"},
		{"description", "<p>Foo.</p>"}, {"update", "true"},
		{"license", "lppl1.3c"}, {"license", "mit"}, {"topic", "colour"},
		{"bugtracker", "https://example.org/issues"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fields() =\n%v\nwant\n%v", got, want)
	}
}

func TestCheck(t *testing.T) {
	s := sample(t)
	if msgs := s.Check(); len(msgs) != 0 {
		t.Fatalf("unexpected messages: %v", msgs)
	}

	s.Pkg = "9foo"
	s.Summary = strings.Repeat("x", 129)
	s.Email = "nope"
	s.Home = []string{"example.org"}
	s.Version = ""
	s.Archive = "foo.rar"
	var texts []string
	for _, m := range s.Check() {
		if m.Severity != SeverityError {
			t.Errorf("expected only errors, got %v", m)
		}
		texts = append(texts, m.Text)
	}
	all := strings.Join(texts, "\n")
	for _, want := range []string{"illegal package name", "summary is too long", "email", "field home", "missing mandatory field version", ".zip, .tgz or .tar.gz"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}

	s = sample(t)
	s.Pkg = "Foo"
	msgs := s.Check()
	if len(msgs) != 1 || msgs[0].Severity != SeverityWarning {
		t.Errorf("uppercase pkg: got %v, want one warning", msgs)
	}
}

func TestClientValidate(t *testing.T) {
	s := sample(t)
	var gotPath string
	var gotForm map[string][]string
	var gotFile, gotFileName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotForm = r.MultipartForm.Value
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
		} else {
			b, _ := io.ReadAll(f)
			gotFile, gotFileName = string(b), h.Filename
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[["WARNING","Topic not found","foo"],["INFO","ok"]]`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	res, err := c.Validate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/validate" {
		t.Errorf("path = %s", gotPath)
	}
	if got := gotForm["license"]; !reflect.DeepEqual(got, []string{"lppl1.3c", "mit"}) {
		t.Errorf("license = %v", got)
	}
	if got := gotForm["author"]; !reflect.DeepEqual(got, []string{"A. Author; B. Author"}) {
		t.Errorf("author = %v", got)
	}
	if gotFile != "PK fake" || gotFileName != "foo.zip" {
		t.Errorf("file = %q (%s)", gotFile, gotFileName)
	}
	if res.HasErrors() {
		t.Errorf("HasErrors() = true for %v", res.Messages)
	}
	if len(res.Messages) != 2 || res.Messages[0].String() != "Topic not found: foo" {
		t.Errorf("messages = %v", res.Messages)
	}
}

func TestClientErrors(t *testing.T) {
	s := sample(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`[["ERROR","Missing field","pkg"]]`))
		default:
			http.Error(w, "no such thing", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}

	res, err := c.Upload(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasErrors() || res.Messages[0].String() != "Missing field: pkg" {
		t.Errorf("409: %+v", res)
	}

	c.BaseURL = srv.URL + "/bogus"
	if _, err := c.Validate(context.Background(), s); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("404: err = %v", err)
	}
}

// Each submission must go over a new connection: reusing the one kept
// alive from validate made the first real upload fail with a bare EOF.
func TestClientFreshConnection(t *testing.T) {
	s := sample(t)
	var mu sync.Mutex
	newConns := 0
	var closeFlags []bool
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		closeFlags = append(closeFlags, r.Close)
		mu.Unlock()
		w.Write([]byte(`[]`))
	}))
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			mu.Lock()
			newConns++
			mu.Unlock()
		}
	}
	srv.Start()
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Validate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if newConns != 2 {
		t.Errorf("new connections = %d, want 2", newConns)
	}
	if !reflect.DeepEqual(closeFlags, []bool{true, true}) {
		t.Errorf("Connection: close = %v, want [true true]", closeFlags)
	}
}
