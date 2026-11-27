#!/bin/sh
# Installs acmd on macOS, Linux or WSL2, with no brew and no sudo:
#
#   curl -fsSL https://raw.githubusercontent.com/brettmostert/agent-command/main/scripts/install.sh | sh
#
# It installs the latest release into ~/.local/share/agent-command/<version>,
# links ~/.local/bin/acmd and ~/.local/bin/agent-command to it, and runs
# acmd init. Running it again upgrades acmd. If acmd from brew is installed,
# it stops: upgrade that one with brew. Options are in usage, below.
#
# A release's checksums.txt names its files and their SHA-256s, so the script
# needs nothing else from the release. If gh is installed and logged in, it
# also checks the download's build attestation.
#
# It's POSIX sh, and all in functions that run on the last line, so a
# download cut short runs nothing.

set -eu

releases_default=https://github.com/brettmostert/homebrew-tap/releases
repo=brettmostert/agent-command
ps1_url=https://raw.githubusercontent.com/brettmostert/agent-command/main/scripts/install.ps1

usage() {
	cat <<'EOF'
Installs acmd, or upgrades it to the latest release.

  curl -fsSL https://raw.githubusercontent.com/brettmostert/agent-command/main/scripts/install.sh | sh
  curl -fsSL …/install.sh | sh -s -- [options]

Options, as flags or environment variables:
  --version <v>  ACMD_VERSION=<v>  install that version, not the latest
  --no-init      ACMD_NO_INIT=1    don't run acmd init
  --uninstall    ACMD_UNINSTALL=1  run acmd uninstall, then remove acmd
  ACMD_INSTALL_URL                 where releases are, for tests and mirrors
EOF
}

say() { printf 'acmd install: %s\n' "$*"; }
die() {
	printf 'acmd install: %s\n' "$*" >&2
	exit 1
}

# on is true for a variable set to anything but "", 0, false or no.
on() {
	case ${1:-} in
	"" | 0 | false | no) return 1 ;;
	esac
}

# platform prints this machine's platform, as release files name it.
platform() {
	os=$(uname -s)
	arch=$(uname -m)
	case $os in
	Darwin)
		# A shell under Rosetta says x86_64 on an Apple Silicon Mac.
		if [ "$arch" = x86_64 ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
			arch=arm64
		fi
		[ "$arch" = arm64 ] || die "acmd needs a Mac with Apple Silicon"
		echo darwin-arm64
		;;
	Linux)
		case $arch in
		x86_64 | amd64) echo linux-amd64 ;;
		aarch64 | arm64) echo linux-arm64 ;;
		*) die "acmd runs on Linux amd64 and arm64, not $arch" ;;
		esac
		;;
	MINGW* | MSYS* | CYGWIN*)
		die "on Windows, run this in PowerShell instead: irm $ps1_url | iex"
		;;
	*) die "acmd runs on macOS, Linux and WSL2, not $os" ;;
	esac
}

fetch() {
	curl -fsSL --retry 3 -o "$2" "$1"
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "there's no sha256sum or shasum to check the download with"
	fi
}

# installed prints the version that bin/acmd links to, if it's this script's.
installed() {
	target=$(readlink "$bin/acmd" 2>/dev/null || true)
	case $target in
	"$data"/*/bin/acmd)
		target=${target#"$data"/}
		echo "${target%%/*}"
		;;
	esac
}

# run_acmd runs acmd from the terminal, if there is one: under curl … | sh,
# stdin is the script.
run_acmd() {
	if (: </dev/tty) 2>/dev/null; then
		"$bin/acmd" "$@" </dev/tty
	else
		"$bin/acmd" "$@" </dev/null
	fi
}

uninstall() {
	current=$(installed)
	if [ -z "$current" ]; then
		say "$bin/acmd isn't one this script installed, so there's nothing to remove"
		return
	fi
	run_acmd uninstall --yes
	rm -f "$bin/acmd" "$bin/agent-command"
	rm -rf "$data"
	say "removed acmd $current"
}

install() {
	plat=$(platform)
	if command -v brew >/dev/null 2>&1; then
		from_brew=$(brew list --versions acmd 2>/dev/null || true)
		if [ -n "$from_brew" ]; then
			die "$from_brew is installed with brew, so this script leaves it alone. Upgrade it with: brew upgrade acmd"
		fi
	fi
	command -v curl >/dev/null 2>&1 || die "this script needs curl"

	want=${ACMD_VERSION:-}
	want=${want#v}
	if [ -n "$want" ]; then
		sums_url="$releases/download/agent-command-v$want/checksums.txt"
	else
		sums_url="$releases/latest/download/checksums.txt"
	fi
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	fetch "$sums_url" "$tmp/checksums.txt" || die "couldn't download $sums_url"
	line=$(grep -E "^[0-9a-f]{64}  acmd-[^ ]+-$plat\.tar\.gz\$" "$tmp/checksums.txt" | head -n 1 || true)
	[ -n "$line" ] || die "the release has no acmd for $plat"
	sum=${line%% *}
	file=${line##* }
	version=${file#acmd-}
	version=${version%-"$plat".tar.gz}
	if [ -n "$want" ] && [ "$version" != "$want" ]; then
		die "asked for $want, but the release has acmd $version"
	fi

	previous=$(installed)
	if [ "$previous" = "$version" ] && [ -x "$data/$version/bin/acmd" ]; then
		say "acmd $version is already installed. Run acmd init to set it up again."
		return
	fi

	say "downloading acmd $version for $plat"
	fetch "$releases/download/agent-command-v$version/$file" "$tmp/$file" || die "couldn't download $file"
	got=$(sha256 "$tmp/$file")
	[ "$got" = "$sum" ] || die "$file's SHA-256 is $got, but the release says $sum"
	if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
		gh attestation verify "$tmp/$file" --repo "$repo" \
			--signer-workflow "$repo/.github/workflows/release.yml" >"$tmp/attestation" 2>&1 ||
			die "$file's build attestation doesn't check out: $(cat "$tmp/attestation")"
		say "checked $file's build attestation"
	fi

	# Unpacked next to where it goes, so the move is a rename. The new acmd
	# must run before anything points at it.
	mkdir -p "$data"
	unpack="$data/.unpack.$$"
	rm -rf "$unpack"
	mkdir "$unpack"
	tar -xzf "$tmp/$file" -C "$unpack"
	new="$unpack/acmd-$version"
	[ -x "$new/bin/acmd" ] || die "$file has no bin/acmd"
	"$new/bin/acmd" version >/dev/null || die "acmd $version doesn't run here"
	rm -rf "${data:?}/$version"
	mv "$new" "$data/$version"
	rm -rf "$unpack"

	# Each link is replaced in one step, so acmd is never missing. The daemon
	# notices the new binary and restarts on it.
	mkdir -p "$bin"
	for name in acmd agent-command; do
		rm -f "$bin/.$name.new"
		ln -s "$data/$version/bin/$name" "$bin/.$name.new"
		mv -f "$bin/.$name.new" "$bin/$name"
	done

	for dir in "$data"/*; do
		[ -d "$dir" ] || continue
		case ${dir##*/} in
		"$version") ;;
		[0-9]*) rm -rf "$dir" ;;
		esac
	done

	if [ -n "$previous" ]; then
		say "upgraded acmd from $previous to $version, in $bin"
	else
		say "installed acmd $version in $bin"
	fi

	if on "${ACMD_NO_INIT:-}"; then
		say "run acmd init to add the Claude Code hooks and the service"
	else
		# Through the link, so the hooks and the service use the link, not
		# this version's folder.
		run_acmd init
	fi

	case ":$PATH:" in
	*":$bin:"*)
		found=$(command -v acmd || true)
		if [ -n "$found" ] && [ "$found" != "$bin/acmd" ]; then
			say "warning: $found comes before $bin/acmd on your PATH"
		fi
		;;
	*)
		say "$bin isn't on your PATH. Add this line to your shell's profile:"
		# shellcheck disable=SC2016 # printed for the user to copy
		printf '  export PATH="$HOME/.local/bin:$PATH"\n'
		;;
	esac
}

main() {
	while [ $# -gt 0 ]; do
		case $1 in
		--version)
			[ $# -ge 2 ] || die "--version needs a version"
			ACMD_VERSION=$2
			shift 2
			;;
		--version=*)
			ACMD_VERSION=${1#*=}
			shift
			;;
		--no-init)
			ACMD_NO_INIT=1
			shift
			;;
		--uninstall)
			ACMD_UNINSTALL=1
			shift
			;;
		-h | --help)
			usage
			return
			;;
		*) die "unknown option $1 (see --help)" ;;
		esac
	done

	[ -n "${HOME:-}" ] || die "HOME isn't set"
	releases=${ACMD_INSTALL_URL:-$releases_default}
	releases=${releases%/}
	data="${XDG_DATA_HOME:-$HOME/.local/share}/agent-command"
	bin="$HOME/.local/bin"
	if on "${ACMD_UNINSTALL:-}"; then
		uninstall
	else
		install
	fi
}

main "$@"
