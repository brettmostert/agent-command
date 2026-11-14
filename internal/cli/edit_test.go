package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditOpensAFileAtALine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ACMD_CONFIG_DIR", dir)
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))
	proj := filepath.Join(dir, "proj")
	must(t, os.MkdirAll(filepath.Join(proj, "src"), 0o755))
	must(t, os.WriteFile(filepath.Join(proj, "src", "main.go"), nil, 0o600))
	must(t, os.WriteFile(filepath.Join(proj, "odd:7"), nil, 0o600))
	editor := filepath.Join(dir, "ed")
	must(t, os.WriteFile(editor, []byte("#!/bin/sh\n"), 0o755)) // #nosec G306 -- an executable for the test

	var launched []string
	old := editLaunch
	editLaunch = func(_ context.Context, argv, _ []string, _ string) error {
		launched = append(launched, strings.Join(argv, " "))
		return nil
	}
	t.Cleanup(func() { editLaunch = old })

	if _, err := acmd(t, "edit", "--in", proj, "src/main.go:3"); err == nil || !strings.Contains(err.Error(), "no editor chosen") {
		t.Fatalf("no editor: %v", err)
	}
	mustRun(t, "config", "set", "editor.target", "cwd")
	mustRun(t, "editor", "set", "custom", "--", editor, "{dir}", "{file}:{line}:{col}")

	main := filepath.Join(proj, "src", "main.go")
	mustRun(t, "edit", "--in", proj, "src/main.go:12:4")
	mustRun(t, "edit", "--in", proj, "src/main.go", "--line", "9")
	mustRun(t, "edit", "--in", proj, "odd:7") // a file really called that
	mustRun(t, "edit", "--in", proj, "src")   // a folder opens as a folder
	want := []string{
		editor + " " + proj + " " + main + ":12:4",
		editor + " " + proj + " " + main + ":9:1",
		editor + " " + proj + " " + filepath.Join(proj, "odd:7") + ":1:1",
		editor + " " + filepath.Join(proj, "src"),
	}
	if strings.Join(launched, "\n") != strings.Join(want, "\n") {
		t.Errorf("launched:\n%s\nwant:\n%s", strings.Join(launched, "\n"), strings.Join(want, "\n"))
	}
	if _, err := acmd(t, "edit", "--in", proj, "src/gone.go:3"); err == nil || !strings.Contains(err.Error(), "gone.go") {
		t.Errorf("missing file: %v", err)
	}

	t.Chdir(proj)
	launched = nil
	mustRun(t, "edit", "src/main.go")
	if len(launched) != 1 || !strings.HasSuffix(launched[0], main+":1:1") {
		t.Errorf("relative to here: %v", launched)
	}
	if f, _, _ := resolveEditPath("/base", "~/notes.md:3"); !strings.HasSuffix(f, "/notes.md") || strings.HasPrefix(f, "/base") {
		t.Errorf("home: %q", f)
	}
	if f, _, _ := resolveEditPath("/base", "~"); strings.HasPrefix(f, "/base") {
		t.Errorf("bare home: %q", f)
	}
}
