//go:build windows

package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// shells are the PowerShells to run install.ps1 in: Windows PowerShell 5.1,
// which every Windows has, and PowerShell 7 where it's installed.
func shells(t *testing.T) map[string]string {
	t.Helper()
	// The script sets the user's PATH in the registry. Each test puts it back,
	// but it's still the real one, so it runs only where asked.
	if os.Getenv("CI") != "true" && os.Getenv("ACMD_TEST_INSTALL") != "1" {
		t.Skip("changes your user PATH (and puts it back): set ACMD_TEST_INSTALL=1 to run it here")
	}
	found := map[string]string{}
	for _, name := range []string{"powershell.exe", "pwsh.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			found[name] = p
		}
	}
	if found["powershell.exe"] == "" {
		t.Fatal("there's no Windows PowerShell")
	}
	return found
}

// keepUserPath puts the user's PATH back as it was when the test ends.
func keepUserPath(t *testing.T) {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	old, typ, err := k.GetStringValue("Path")
	missing := errors.Is(err, registry.ErrNotExist)
	if err != nil && !missing {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer k.Close()
		switch {
		case missing:
			_ = k.DeleteValue("Path")
		case typ == registry.EXPAND_SZ:
			_ = k.SetExpandStringValue("Path", old)
		default:
			_ = k.SetStringValue("Path", old)
		}
	})
}

func userPath(t *testing.T) []string {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		t.Fatal(err)
	}
	return strings.Split(v, ";")
}

// winSandbox is a LOCALAPPDATA and a Scoop folder for install.ps1, with its
// own PATH.
type winSandbox struct {
	t       *testing.T
	shell   string
	tap     *tap
	local   string
	scoop   string
	fakebin string
	log     string
}

func newWinSandbox(t *testing.T, shell string, tp *tap) *winSandbox {
	t.Helper()
	keepUserPath(t)
	dir := t.TempDir()
	s := &winSandbox{t: t, shell: shell, tap: tp, local: filepath.Join(dir, "local"), scoop: filepath.Join(dir, "scoop"),
		fakebin: filepath.Join(dir, "fakebin"), log: filepath.Join(dir, "acmd.log")}
	for _, d := range []string{s.local, s.scoop, s.fakebin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// run pipes install.ps1 into iex, as irm … | iex does.
func (s *winSandbox) run(env ...string) (string, error) {
	s.t.Helper()
	script, err := filepath.Abs(`..\..\scripts\install.ps1`)
	if err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command(s.shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", "Get-Content -Raw -LiteralPath '"+script+"' | Invoke-Expression")
	root := os.Getenv("SystemRoot")
	cmd.Env = append(os.Environ(),
		"LOCALAPPDATA="+s.local,
		"SCOOP="+s.scoop,
		"SCOOP_GLOBAL="+filepath.Join(s.scoop, "global"),
		// No gh: GitHub's runners have one, and it can't check a fake build.
		"PATH="+strings.Join([]string{s.fakebin, filepath.Join(root, "System32"), root,
			filepath.Join(root, `System32\WindowsPowerShell\v1.0`)}, ";"),
		"TEMP="+s.t.TempDir(),
		"TMP="+s.t.TempDir(),
		"ACMD_INSTALL_URL="+s.tap.url,
		"FAKEACMD_LOG="+s.log,
		"ACMD_VERSION=", "ACMD_NO_INIT=", "ACMD_UNINSTALL=",
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *winSandbox) mustRun(env ...string) string {
	s.t.Helper()
	out, err := s.run(env...)
	if err != nil {
		s.t.Fatalf("install.ps1 failed: %v\n%s", err, out)
	}
	return out
}

func (s *winSandbox) mustFail(env ...string) string {
	s.t.Helper()
	out, err := s.run(env...)
	if err == nil {
		s.t.Fatalf("install.ps1 succeeded, but it shouldn't have:\n%s", out)
	}
	return out
}

func (s *winSandbox) root(parts ...string) string {
	return filepath.Join(append([]string{s.local, "Programs", "agent-command"}, parts...)...)
}

// pointsAt checks current is a junction to version's folder, with both of
// acmd's programs, and that acmd runs through it.
func (s *winSandbox) pointsAt(version string) {
	s.t.Helper()
	target, err := os.Readlink(s.root("current"))
	if err != nil {
		s.t.Fatalf("current isn't a junction: %v", err)
	}
	target = filepath.Clean(strings.TrimPrefix(target, `\\?\`))
	if want := s.root(version); !strings.EqualFold(target, want) {
		s.t.Fatalf("current points at %s, not %s", target, want)
	}
	for _, exe := range []string{"acmd.exe", "acmdw.exe"} {
		if _, err := os.Stat(s.root("current", exe)); err != nil {
			s.t.Fatal(err)
		}
	}
	out, err := exec.Command(s.root("current", "acmd.exe"), "version").Output()
	if err != nil || !strings.Contains(string(out), "agent-command "+version+" ") {
		s.t.Fatalf("acmd version says %q (%v), not %s", out, err, version)
	}
}

func (s *winSandbox) onUserPath() bool {
	for _, p := range userPath(s.t) {
		if strings.EqualFold(p, s.root("current")) {
			return true
		}
	}
	return false
}

func TestPs1InstallsTheLatest(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.1.0"), newRelease(t, "0.2.0")))
			out := s.mustRun()

			mustContain(t, out, "installed acmd 0.2.0")
			s.pointsAt("0.2.0")
			if !s.onUserPath() {
				t.Fatalf("%s isn't on the user's PATH: %q", s.root("current"), userPath(t))
			}
			// acmd sees itself in current, as under Scoop, so the logon task
			// and the hooks name the path that upgrades keep.
			got := logLines(t, s.log)
			if len(got) != 1 || !strings.HasPrefix(got[0], "0.2.0 init ") ||
				!strings.HasSuffix(strings.ToLower(got[0]), strings.ToLower(" exe="+s.root("current", "acmd.exe"))) {
				t.Fatalf("acmd ran as %q, want init, from %s", got, s.root("current", "acmd.exe"))
			}
		})
	}
}

func TestPs1UpgradesAndRemovesTheOldVersion(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.1.0"), newRelease(t, "0.2.0")))
			s.mustRun("ACMD_VERSION=0.1.0")
			s.pointsAt("0.1.0")

			out := s.mustRun()
			mustContain(t, out, "upgraded acmd from 0.1.0 to 0.2.0")
			s.pointsAt("0.2.0")
			notExist(t, s.root("0.1.0"))
			if n := strings.Count(strings.Join(userPath(t), ";"), s.root("current")); n != 1 {
				t.Fatalf("current is on the user's PATH %d times, want once", n)
			}
		})
	}
}

func TestPs1LeavesTheSameVersionAlone(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			rel := newRelease(t, "0.2.0")
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", rel))
			s.mustRun()
			mustContain(t, s.mustRun(), "acmd 0.2.0 is already installed")
			if n := s.tap.downloaded(rel.file); n != 1 {
				t.Fatalf("%s was downloaded %d times, want once", rel.file, n)
			}
		})
	}
}

func TestPs1SkipsInitWhenAsked(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
			mustContain(t, s.mustRun("ACMD_NO_INIT=1"), "run acmd init")
			s.pointsAt("0.2.0")
			if got := logLines(t, s.log); got != nil {
				t.Fatalf("acmd ran as %q, but init was off", got)
			}
		})
	}
}

func TestPs1StopsForScoopsAcmd(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
			manifest := filepath.Join(s.scoop, "apps", "acmd", "current", "manifest.json")
			if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifest, []byte(`{"version": "0.0.9"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			out := s.mustFail()

			mustContain(t, out, "acmd 0.0.9 is installed with Scoop")
			mustContain(t, out, "scoop update acmd")
			notExist(t, s.root())
		})
	}
}

func TestPs1RefusesABadDownload(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.2.0").corrupt()))
			mustContain(t, s.mustFail(), "SHA-256")
			notExist(t, s.root())
		})
	}
}

func TestPs1NamesAMissingVersion(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
			mustContain(t, s.mustFail("ACMD_VERSION=9.9.9"), "couldn't download "+s.tap.url+"/download/agent-command-v9.9.9/checksums.txt")
			notExist(t, s.root())
		})
	}
}

func TestPs1Uninstalls(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
			s.mustRun()
			out := s.mustRun("ACMD_UNINSTALL=1")

			mustContain(t, out, "removed acmd 0.2.0")
			got := logLines(t, s.log)
			if len(got) != 2 || !strings.HasPrefix(got[1], "0.2.0 uninstall --yes ") {
				t.Fatalf("acmd ran as %q, want init, then uninstall --yes", got)
			}
			notExist(t, s.root())
			if s.onUserPath() {
				t.Fatalf("%s is still on the user's PATH", s.root("current"))
			}
		})
	}
}

func TestPs1UninstallWithNothingInstalled(t *testing.T) {
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			s := newWinSandbox(t, shell, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
			mustContain(t, s.mustRun("ACMD_UNINSTALL=1"), "nothing to remove")
		})
	}
}
