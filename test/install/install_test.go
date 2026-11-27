// Package install tests scripts/install.sh and scripts/install.ps1 as people
// run them, piped into sh or into iex, against a fake tap release on
// 127.0.0.1. acmd is a fake (testdata/fakeacmd) that records what the scripts
// run it with.
package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakes holds fakeacmd built for each version a test release has.
var fakes = map[string][]byte{}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "acmd-install-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, v := range []string{"0.1.0", "0.2.0"} {
		out := filepath.Join(dir, "fakeacmd-"+v)
		cmd := exec.Command("go", "build", "-o", out, "-ldflags", "-X main.version="+v, "./testdata/fakeacmd")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building fakeacmd %s: %v\n%s", v, err, b)
			os.Exit(1)
		}
		if fakes[v], err = os.ReadFile(out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// platforms are the platforms releases have files for, as pack-acmd.sh and
// pack-windows.sh name them.
var platforms = []string{"darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}

// hostPlatform is this machine's platform, if releases have one for it.
func hostPlatform(t *testing.T) string {
	t.Helper()
	p := runtime.GOOS + "-" + runtime.GOARCH
	for _, q := range platforms {
		if p == q {
			return p
		}
	}
	t.Skipf("releases have no acmd for %s", p)
	return ""
}

func archiveName(version, platform string) string {
	if strings.HasPrefix(platform, "windows-") {
		return "acmd-" + version + "-" + platform + ".zip"
	}
	return "acmd-" + version + "-" + platform + ".tar.gz"
}

// release is a fake tap release: this machine's archive, laid out as the
// real one is, and a checksums.txt that lists every platform's.
type release struct {
	version string
	file    string
	archive []byte
	sums    string
}

func newRelease(t *testing.T, version string) *release {
	t.Helper()
	platform := hostPlatform(t)
	r := &release{version: version, file: archiveName(version, platform)}
	if strings.HasPrefix(platform, "windows-") {
		r.archive = zipOf(t, map[string][]byte{"acmd.exe": fakes[version], "acmdw.exe": fakes[version]})
	} else {
		top := "acmd-" + version + "/"
		r.archive = tarGzOf(t, top, map[string][]byte{
			"bin/acmd":             fakes[version],
			"bin/agent-command":    fakes[version],
			"libexec/acmd-speech":  []byte("#!/bin/sh\n"),
			"libexec/mlx.metallib": []byte("shaders"),
		})
	}
	var sums strings.Builder
	fmt.Fprintf(&sums, "%064x  AgentCommand.zip\n", 1)
	for _, p := range platforms {
		if p == platform {
			fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(r.archive), r.file)
		} else {
			fmt.Fprintf(&sums, "%064x  %s\n", 2, archiveName(version, p))
		}
	}
	r.sums = sums.String()
	return r
}

// corrupt makes the archive differ from its SHA-256 in checksums.txt.
func (r *release) corrupt() *release {
	r.archive = append(append([]byte{}, r.archive...), 0)
	return r
}

func tarGzOf(t *testing.T, top string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, dir := range []string{top, top + "bin/", top + "libexec/"} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: top + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tap serves releases as the tap's releases URL does: latest/download/<file>
// and download/agent-command-v<version>/<file>.
type tap struct {
	url string

	mu        sync.Mutex
	downloads map[string]int // archive name → times downloaded
}

func newTap(t *testing.T, latest string, releases ...*release) *tap {
	t.Helper()
	byVersion := map[string]*release{}
	for _, r := range releases {
		byVersion[r.version] = r
	}
	tp := &tap{downloads: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var version, name string
		switch {
		case strings.HasPrefix(req.URL.Path, "/latest/download/"):
			version, name = latest, strings.TrimPrefix(req.URL.Path, "/latest/download/")
		case strings.HasPrefix(req.URL.Path, "/download/agent-command-v"):
			version, name, _ = strings.Cut(strings.TrimPrefix(req.URL.Path, "/download/agent-command-v"), "/")
		}
		r := byVersion[version]
		switch {
		case r == nil:
			http.NotFound(w, req)
		case name == "checksums.txt":
			_, _ = w.Write([]byte(r.sums))
		case name == r.file:
			tp.mu.Lock()
			tp.downloads[name]++
			tp.mu.Unlock()
			_, _ = w.Write(r.archive)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	tp.url = srv.URL
	return tp
}

func (tp *tap) downloaded(name string) int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.downloads[name]
}

// logLines is what fakeacmd recorded: one line per run that wasn't
// `acmd version`.
func logLines(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func mustContain(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Fatalf("the output doesn't say %q:\n%s", want, out)
	}
}

func notExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists (err %v)", path, err)
	}
}
