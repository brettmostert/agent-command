//go:build !windows

package install

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGh stands in for gh: logged in only with FAKEGH_LOGGED_IN=1, and an
// attestation that checks out unless FAKEGH_VERIFY_FAIL=1. It's always on
// PATH, so a real gh (GitHub's runners have one in /usr/bin) never runs.
const fakeGh = `#!/bin/sh
case "$1 $2" in
"auth status") [ "${FAKEGH_LOGGED_IN:-}" = 1 ] ;;
"attestation verify")
	echo "$*" >>"$FAKEGH_LOG"
	if [ "${FAKEGH_VERIFY_FAIL:-}" = 1 ]; then echo "no attestation matched" >&2; exit 1; fi ;;
*) exit 2 ;;
esac
`

// sandbox is a home folder for install.sh, with its own PATH.
type sandbox struct {
	t       *testing.T
	tap     *tap
	home    string
	fakebin string
	log     string
	ghLog   string
}

func newSandbox(t *testing.T, tp *tap) *sandbox {
	t.Helper()
	dir := t.TempDir()
	s := &sandbox{t: t, tap: tp, home: filepath.Join(dir, "home"), fakebin: filepath.Join(dir, "fakebin"),
		log: filepath.Join(dir, "acmd.log"), ghLog: filepath.Join(dir, "gh.log")}
	for _, d := range []string{s.home, s.fakebin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.fake("gh", fakeGh)
	return s
}

// fake puts a script named name first on the sandbox's PATH.
func (s *sandbox) fake(name, script string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.fakebin, name), []byte(script), 0o755); err != nil {
		s.t.Fatal(err)
	}
}

// run pipes install.sh into sh, as curl … | sh does.
func (s *sandbox) run(env []string, args ...string) (string, error) {
	s.t.Helper()
	script, err := os.ReadFile("../../scripts/install.sh")
	if err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", append([]string{"-s", "--"}, args...)...)
	cmd.Stdin = bytes.NewReader(script)
	cmd.Env = append([]string{
		"HOME=" + s.home,
		"PATH=" + s.fakebin + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + s.t.TempDir(),
		"ACMD_INSTALL_URL=" + s.tap.url,
		"FAKEACMD_LOG=" + s.log,
		"FAKEGH_LOG=" + s.ghLog,
	}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *sandbox) mustRun(env []string, args ...string) string {
	s.t.Helper()
	out, err := s.run(env, args...)
	if err != nil {
		s.t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	return out
}

func (s *sandbox) mustFail(env []string, args ...string) string {
	s.t.Helper()
	out, err := s.run(env, args...)
	if err == nil {
		s.t.Fatalf("install.sh succeeded, but it shouldn't have:\n%s", out)
	}
	return out
}

func (s *sandbox) bin(name string) string { return filepath.Join(s.home, ".local", "bin", name) }

func (s *sandbox) data(parts ...string) string {
	return filepath.Join(append([]string{s.home, ".local", "share", "agent-command"}, parts...)...)
}

// linksTo checks bin/acmd and bin/agent-command link into version's folder,
// and that acmd runs through the link.
func (s *sandbox) linksTo(version string) {
	s.t.Helper()
	for _, name := range []string{"acmd", "agent-command"} {
		target, err := os.Readlink(s.bin(name))
		if err != nil {
			s.t.Fatalf("%s isn't a link: %v", s.bin(name), err)
		}
		if want := s.data(version, "bin", name); target != want {
			s.t.Fatalf("%s links to %s, not %s", name, target, want)
		}
	}
	out, err := exec.Command(s.bin("acmd"), "version").Output()
	if err != nil || !strings.Contains(string(out), "agent-command "+version+" ") {
		s.t.Fatalf("acmd version says %q (%v), not %s", out, err, version)
	}
}

func (s *sandbox) nothingInstalled() {
	s.t.Helper()
	notExist(s.t, s.bin("acmd"))
	notExist(s.t, s.bin("agent-command"))
	notExist(s.t, s.data())
}

func TestShInstallsTheLatest(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.1.0"), newRelease(t, "0.2.0")))
	out := s.mustRun(nil)

	mustContain(t, out, "installed acmd 0.2.0")
	s.linksTo("0.2.0")
	// The whole folder comes, so acmd finds the speech engine in ../libexec.
	if _, err := os.Stat(s.data("0.2.0", "libexec", "acmd-speech")); err != nil {
		t.Fatal(err)
	}
	// acmd init runs through the link, so the hooks and the service use it.
	if got, want := s.runs(), []string{"0.2.0 init argv0=" + s.bin("acmd")}; !sameLog(got, want) {
		t.Fatalf("acmd ran as %q, want %q", got, want)
	}
	mustContain(t, out, s.bin("")+" isn't on your PATH")
}

func TestShUpgradesAndRemovesTheOldVersion(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.1.0"), newRelease(t, "0.2.0")))
	s.mustRun([]string{"ACMD_VERSION=0.1.0"})
	s.linksTo("0.1.0")

	out := s.mustRun(nil)
	mustContain(t, out, "upgraded acmd from 0.1.0 to 0.2.0")
	s.linksTo("0.2.0")
	notExist(t, s.data("0.1.0"))
	if got := s.runs(); len(got) != 2 || !strings.HasPrefix(got[1], "0.2.0 init ") {
		t.Fatalf("acmd ran as %q, want init by 0.1.0 then by 0.2.0", got)
	}
}

func TestShLeavesTheSameVersionAlone(t *testing.T) {
	rel := newRelease(t, "0.2.0")
	s := newSandbox(t, newTap(t, "0.2.0", rel))
	s.mustRun(nil)
	out := s.mustRun(nil)

	mustContain(t, out, "acmd 0.2.0 is already installed")
	if n := s.tap.downloaded(rel.file); n != 1 {
		t.Fatalf("%s was downloaded %d times, want once", rel.file, n)
	}
	if got := s.runs(); len(got) != 1 {
		t.Fatalf("acmd ran as %q, want one init", got)
	}
}

func TestShOptions(t *testing.T) {
	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"flags":     {args: []string{"--version", "v0.1.0", "--no-init"}},
		"flag=":     {args: []string{"--version=0.1.0", "--no-init"}},
		"variables": {env: []string{"ACMD_VERSION=0.1.0", "ACMD_NO_INIT=1"}},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.1.0"), newRelease(t, "0.2.0")))
			out := s.mustRun(c.env, c.args...)
			s.linksTo("0.1.0")
			mustContain(t, out, "run acmd init")
			if got := s.runs(); got != nil {
				t.Fatalf("acmd ran as %q, but init was off", got)
			}
		})
	}
}

func TestShStopsForBrewsAcmd(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	s.fake("brew", "#!/bin/sh\n[ \"$*\" = 'list --versions acmd' ] && echo 'acmd 0.0.9'\nexit 0\n")
	out := s.mustFail(nil)

	mustContain(t, out, "acmd 0.0.9 is installed with brew")
	mustContain(t, out, "brew upgrade acmd")
	s.nothingInstalled()
}

func TestShIgnoresBrewWithoutAcmd(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	s.fake("brew", "#!/bin/sh\nexit 1\n")
	s.mustRun(nil)
	s.linksTo("0.2.0")
}

func TestShReplacesAnotherAcmd(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	if err := os.MkdirAll(s.bin(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.bin("acmd"), []byte("#!/bin/sh\necho a dev build\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.mustRun(nil)
	s.linksTo("0.2.0")
}

func TestShSaysWhenAnotherAcmdComesFirst(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	s.fake("acmd", "#!/bin/sh\n")
	out := s.mustRun([]string{"PATH=" + s.fakebin + ":" + s.bin("") + ":/usr/bin:/bin:/usr/sbin:/sbin"})

	mustContain(t, out, filepath.Join(s.fakebin, "acmd")+" comes before "+s.bin("acmd"))
	if strings.Contains(out, "isn't on your PATH") {
		t.Fatalf("it says ~/.local/bin isn't on PATH, but it is:\n%s", out)
	}
}

func TestShRefusesABadDownload(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0").corrupt()))
	out := s.mustFail(nil)

	mustContain(t, out, "SHA-256")
	s.nothingInstalled()
}

func TestShChecksTheAttestationWithGh(t *testing.T) {
	rel := newRelease(t, "0.2.0")
	s := newSandbox(t, newTap(t, "0.2.0", rel))
	out := s.mustRun([]string{"FAKEGH_LOGGED_IN=1"})

	mustContain(t, out, "checked "+rel.file+"'s build attestation")
	got := logLines(t, s.ghLog)
	if len(got) != 1 || !strings.HasSuffix(got[0], rel.file+" --repo brettmostert/agent-command "+
		"--signer-workflow brettmostert/agent-command/.github/workflows/release.yml") {
		t.Fatalf("gh ran as %q", got)
	}
	s.linksTo("0.2.0")
}

func TestShRefusesABadAttestation(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	out := s.mustFail([]string{"FAKEGH_LOGGED_IN=1", "FAKEGH_VERIFY_FAIL=1"})

	mustContain(t, out, "build attestation doesn't check out: no attestation matched")
	s.nothingInstalled()
}

func TestShSkipsTheAttestationWhenGhIsntLoggedIn(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	out := s.mustRun(nil)

	if strings.Contains(out, "attestation") || logLines(t, s.ghLog) != nil {
		t.Fatalf("it checked the attestation with a gh that isn't logged in:\n%s", out)
	}
}

func TestShNamesAMissingVersion(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	out := s.mustFail([]string{"ACMD_VERSION=9.9.9"})

	mustContain(t, out, "couldn't download "+s.tap.url+"/download/agent-command-v9.9.9/checksums.txt")
	s.nothingInstalled()
}

func TestShRefusesOtherPlatforms(t *testing.T) {
	for name, c := range map[string]struct{ os, arch, want string }{
		"riscv":   {"Linux", "riscv64", "not riscv64"},
		"intel":   {"Darwin", "x86_64", "a Mac with Apple Silicon"},
		"freebsd": {"FreeBSD", "amd64", "not FreeBSD"},
		"windows": {"MINGW64_NT-10.0", "x86_64", "irm https://raw.githubusercontent.com/brettmostert/agent-command/main/scripts/install.ps1 | iex"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
			s.fake("uname", "#!/bin/sh\ncase $1 in -s) echo '"+c.os+"' ;; -m) echo '"+c.arch+"' ;; esac\n")
			// sysctl says this "Mac" has no Apple Silicon, under Rosetta or not.
			s.fake("sysctl", "#!/bin/sh\necho 0\n")
			out := s.mustFail(nil)
			mustContain(t, out, c.want)
			s.nothingInstalled()
		})
	}
}

func TestShUninstalls(t *testing.T) {
	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"flag":     {args: []string{"--uninstall"}},
		"variable": {env: []string{"ACMD_UNINSTALL=1"}},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
			s.mustRun(nil)
			out := s.mustRun(c.env, c.args...)

			mustContain(t, out, "removed acmd 0.2.0")
			got := s.runs()
			if len(got) != 2 || got[1] != "0.2.0 uninstall --yes argv0="+s.bin("acmd") {
				t.Fatalf("acmd ran as %q, want init, then uninstall --yes", got)
			}
			s.nothingInstalled()
		})
	}
}

func TestShUninstallLeavesAnotherAcmdAlone(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	if err := os.MkdirAll(s.bin(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.bin("acmd"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := s.mustRun(nil, "--uninstall")

	mustContain(t, out, "nothing to remove")
	if _, err := os.Stat(s.bin("acmd")); err != nil {
		t.Fatalf("it removed the other acmd: %v", err)
	}
}

func TestShRejectsUnknownOptions(t *testing.T) {
	s := newSandbox(t, newTap(t, "0.2.0", newRelease(t, "0.2.0")))
	mustContain(t, s.mustFail(nil, "--yolo"), "unknown option --yolo")
	mustContain(t, s.mustRun(nil, "--help"), "ACMD_VERSION")
	s.nothingInstalled()
}

// runs is what fakeacmd recorded, without exe=, which on Unix is the
// versioned path the link resolves to.
func (s *sandbox) runs() []string {
	s.t.Helper()
	var got []string
	for _, line := range logLines(s.t, s.log) {
		line, _, _ = strings.Cut(line, " exe=")
		got = append(got, line)
	}
	return got
}

func sameLog(got, want []string) bool {
	return strings.Join(got, "\n") == strings.Join(want, "\n")
}
