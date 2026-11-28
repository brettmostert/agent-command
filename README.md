# Agent Command

A native macOS app for running Claude Code sessions and terminals side by side, built on [libghostty](https://github.com/ghostty-org/ghostty). Sessions keep running when you close a pane or quit the app, and show live status: busy, waiting on you, done, paused or error.

## Features

- **Panes:** split sessions horizontally and vertically to any depth. Sessions created with splits stay together as a set.
- **Keyboard:** click a session or press ⇧⌘B to move the keyboard to the sidebar. The sidebar, the Inbox, the popup and Scheduled share their list keys: the arrow keys, ⌃J and ⌃K, or j and k move, X marks, Return opens, Tab goes to the next that needs you (a session in the sidebar, an unread message in the Inbox), ⌫ dismisses or deletes and Esc goes back. In the sidebar, 1 to 9 open that session. ⌘R renames and ⇧⌘⌫ archives (for a session added from a Remote host, ⌥⌘⌫ removes it from this Mac and leaves it running there), acting on what's marked, the row in the sidebar, or the session you're in; in Scheduled, the same keys edit and delete the job, and ⌘W closes the Inbox or Scheduled. Renaming several numbers them: `name - 1`, `name - 2`.
- **Keyboard shortcuts** (⌘/, or **Keyboard Shortcuts** in the Help menu): every shortcut in one list, grouped by where it works. `acmd keys` prints the same list in a terminal (`--markdown` or `--json` too). Click **+** beside one and press a key to add or change it, **×** on a key to remove it, or the arrow to put back the default. It says when two actions share a key, and won't take a key that would stop a terminal or a text field getting it.
- **Settings** (⌘,): every setting in one window. **General:** open at login, the menu bar icon, Agent Remote Control for new sessions, and warnings you dismissed. **Harnesses:** what sessions and jobs run in: add, rename and remove them. Each is Claude or Antigravity with its own name, program and extra arguments; each Claude harness has its own folder (`CLAUDE_CONFIG_DIR`, with a folder picker and New Folder), so two can be signed in to different accounts. `acmd harness` does the same from a terminal. **Notifications:** what to be told about, how long a turn must take, how often per session, and the sound. **Reply Popup:** on or off, what opens it, its style, and reading aloud. **Read Aloud:** speech on or off, the voice, the speed and a sample. **Scheduled Jobs:** waking the Mac. **Appearance:** the theme, and the terminal's font and size. **Keyboard:** the shortcuts list. **Editor:** where `acmd` opens folders. **Remote:** Hosting, this Mac's identity, clients and known hosts for Remote sessions. **Advanced:** checking and repairing the setup, and the settings files. acmd's settings live in `~/.config/agent-command/config.toml`, which `acmd config show` lists and `acmd config set` changes (`acmd config set notify.cooldown 1m`). The daemon is the file's only writer and keeps its comments; a change, in the app, with `acmd` or by hand, applies straight away.
- **Menu bar icon:** opens the app, the Inbox (with how many messages are waiting), Scheduled Jobs, a new Claude session, Settings and the keyboard shortcuts, and turns the reply popup on or off, from any app, even with the window closed. Turn it off in Settings → General.
- **Going to a session:** ⌘1 to ⌘9 show a session, counting down the sidebar, from anywhere in the app. ⇧⌘A goes to the next one that needs you: waiting, then errors, then finished turns you haven't seen. ⌃⌘A brings the app forward from any app, and hides it again when you're in it.
- **Your editor:** ⇧⌘E (or **Open in Editor** in a session's menu, the File menu or the command popup) opens the session's folder in the editor chosen in Settings → Editor: VS Code, Cursor, Zed, IntelliJ IDEA or your own command. ⌘-click a file path in a terminal, such as `src/app/main.swift:42` in Claude's output, and it opens at that line, in the session's folder's window. A path is only ever opened in your editor, never run. From a terminal: `acmd open <session>` and `acmd edit <file[:line[:col]]>`.
- **Live status** in the sidebar, including an agent you start by typing `claude` in a terminal.
- **Sessions that last:** closing a pane only detaches. Only archiving ends a session, and agents can be unarchived and resumed.
- **Runs in the background:** closing the main window keeps the app running, so the popup and Dock badge still work. Click the Dock icon to bring the window back, and press ⌘Q to quit.
- **Inbox** (⇧⌘I) with what your agents have sent you: finished turns, questions, permissions and errors. Read, reply, answer or go to the session, by session, by set or as one queue.
- **Scheduled jobs** (⇧⌘J, or **Scheduled** in the status bar): run a prompt on a schedule. Choose how often (every few minutes or hours, every day, weekdays, days of the week or of the month, or once) and when, and the form writes the cron; or type cron, and the choices follow it. Either way it shows the schedule in plain English and its next runs. A job runs in a folder, in a new git worktree of a repo so it can't touch your own work, or in an existing session, continuing its conversation. Write the prompt in a Markdown editor with a toolbar, shortcuts and a preview, or point it at a Markdown file that's read at each run. Set Claude Code's model, effort, permission mode, tools, extra folders, system prompt, MCP config and settings file per job. Each run is a session like any other, with a clock beside its name in the sidebar and the Inbox (so is a session a job continues); its tooltip names the job. Runs the Mac sleeps through show as missed, or run when it wakes if the job says so. Or have acmd wake the Mac two minutes before a run, for every job or the jobs you choose: turning it on (when the app is first set up, from the warning it shows while jobs can't wake the Mac, from **Wake the Mac** below the job list, at the end of `acmd init` in a terminal, or with `acmd schedule wake on`) asks for your password once, to add a sudoers rule (`/etc/sudoers.d/agent-command`) that lets you set and cancel acmd's own wakes, and nothing else, without one. The Mac stays awake while a woken run works. A MacBook with its lid closed on battery may not wake. `acmd schedule wake off` clears acmd's wakes and removes the rule, as does `acmd uninstall`. acmd's daemon runs them, so they run with the window closed, and its service starts it at login, so they run after a restart too. From the terminal, `acmd schedule` lists, adds, edits, pauses, resumes, runs and deletes jobs (`acmd schedule add "Morning triage" --cron "0 9 * * 1-5" --prompt "Triage new issues"`), and `acmd schedule explain "<cron>"` says a schedule in English.
- **Reply popup:** when you're in another app, what your agents send opens in a window over everything, without taking your typing. Click another app and it drops behind so you can carry on; a new message brings it back. Click it or press ⌃⌘O to use it, and ⌃⌘O opens it from any app. The bell can keep it always on top over a dimmed screen instead. Move through the queue, then answer, reply or go to the session. Turn it off with ⌥⌘I or the bell in the status bar, which also chooses what opens it. While it's off, or for what it doesn't open, you get notifications instead.
- **Read aloud:** a local voice ([Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M) on MLX) reads messages in the popup, the Inbox and the agent view, with the speaker button or ⇧⌘S. Turn it on from the bell, which downloads the model (about 360 MB). The popup can read each message as it's shown. American English voices only for now.
- **Dock icon:** shows how many messages are unread, and its right-click menu opens the Inbox. Clicking it brings up the popup if that's open, otherwise the main window.
- **Notifications** when a session needs you, fails or finishes a long turn. Work Claude runs in the background counts too: when a 10-minute build it started in the background finishes, you hear about it, and until then its row says `done · background work`. Clicking one opens that session. After quitting with ⌘Q, they still appear, but a click can't open the session.
- **Resume** a session from the palette's Resume session: the stopped and archived sessions here and on your Remote hosts, and Claude and Antigravity sessions acmd never ran (one from a plain terminal, or a purged one). **Where** at the top (All, Local, Remote or a host, as the sidebar has it; Tab moves through it) picks which machines to list. A search matches the name first, then the id, then the folder. A session resumes on the machine it's on. Paste an agent's session id to look it up.
- **Harness names:** each session's harness ("Claude", "Work", "Terminal") shows under its status in the sidebar, and after its status over its pane. A host's session shows the host's name for its harness.
- **Command popup** and a live **theme picker**, using Ghostty's themes.
- **Agent Remote Control:** when it's on, every Claude and Antigravity session starts with `--remote-control`, so you can use it from claude.ai, the Claude app or Antigravity. Turn it on in Settings or with `acmd agent-remote-control on` (it was `acmd remote-control`, and the `remote_control` setting is now `agent_remote_control`).
- **Remote sessions:** use the sessions that run on another Mac's Agent Command, or let another Mac use yours. See [Remote sessions](#remote-sessions) below.
- **Report a bug or request a feature** from the Help menu or the command popup. It opens a filled-in GitHub issue in your browser for you to submit. A bug report can include the last 200 lines of the daemon log, with secrets, your account name and your home folder taken out, and you see what will be included first.
- `acmd` brings up the app, or its window if it's already running. `acmd --new-instance` starts another copy, and its subcommands list and manage sessions.

> **Status:** early development.

## Remote sessions

Pair another Agent Command with this one, and it shows this Mac's sessions, groups and sets as you have them here, opens them, types in them and gets their notifications, while they keep running here. The Mac with the sessions turns on **Hosting** (Settings → Remote, or `acmd remote hosting on`), and the other adds it as a known host by its address and fingerprint (`acmd remote add`). The two connect with mutual TLS, each pinning the other's fingerprint, and the host approves each new client. Hosting is off by default, and listens only on localhost until you choose otherwise.

[docs/remote-sessions.md](docs/remote-sessions.md) has a quick start for a local network, a guide for the internet through Cloudflare Tunnel, your own certs, the security notes, every `acmd remote` command and exit code, and troubleshooting. Remote sessions are separate from Agent Remote Control, which is for claude.ai, the Claude app and Antigravity.

## Scripting with acmd

Every command that reads or changes state takes `--json`, which prints the answer of the daemon op it runs, in the daemon's wire types (`internal/ipc`), and nothing else. A command that runs several ops prints one answer per line. `acmd events` follows the daemon as it changes: a snapshot, then each session, group, inbox item, job or setting added, updated or removed, one JSON event per line (`--types` to pick, `--since` to resume). Errors go to stderr as `{"error": {"code": "…", "message": "…"}}`. Commands take ids, never names or numbers in a list: `acmd list` shows each session's id, and `acmd find <name>` (or `acmd group find <name>`) prints the ids of those whose name has it. How `acmd list` lays sessions out is its own flags' (`--mode`, `--sort`, `--desc`): the daemon keeps no view, and each client keeps its own.

`acmd list` filters too. Each filter is a flag, or `--filter key=value`, which is the same filter; `--filter` with no key is a text search. Different filters must all match, and one given twice matches either value:

| Key | Flag | Values |
|---|---|---|
| `on` | `--on` | `local`, `remote` (any Remote host), or a host's name |
| `resumable` | `--resumable` | what can be resumed: stopped sessions, here and on your hosts, and agents' sessions acmd never ran (no acmd id) |
| `harness` | `--harness` | a harness's name, such as `Work` or `Terminal` |
| `group` | `--group` | a group's id |
| `status` | `--status` | a status, such as `busy` or `waiting` (comma-separated, or repeated) |
| `archived` | `--archived` | `true`: include archived sessions |
| `text` | `--filter <text>` | text in the name, description, folder or group (`g:<text>` for the group only) |

```sh
acmd list --on studio --resumable --harness Work
acmd list --filter on=studio --filter resumable=true --filter harness=Work   # the same
```

`--on` filters the list here; `--host` (on every command) runs the whole command on that host instead. `acmd resume <id>` resumes a stopped session, or, by its agent session id as `acmd list --resumable` shows it, a session acmd never ran: here, or on the host it's on.

**`acmd` on its own** opens a UI. That's the app when this machine has one and you're at it: the Mac app, or the Linux UI on a desktop. Otherwise it's the terminal UI: over SSH, on Windows, or on a machine with no screen. `acmd --app` and `acmd --terminal` choose. The terminal UI lists the sessions here, then each host's, grouped as in the app's sidebar, with what's waiting:

- arrows or `j`/`k` move, and Enter attaches (Ctrl+] comes back to the list);
- `a` answers the selected session's prompt;
- `n` starts a session, here or on the selected session's host;
- `/` filters, and `q` quits.

It follows changes as they happen, shows each notice, and rings the bell for the ones sent to this terminal.

When no app or terminal client shows a notice, the OS does, where there's a desktop: terminal-notifier or a notification on macOS, `notify-send` on a Linux desktop, and a toast on Windows (it names Windows PowerShell as its app, because acmd registers none of its own). A machine with no desktop shows none: the notice still reaches your terminal clients, or the machine you're using.

From any terminal, on macOS, Linux and Windows:

- **`acmd attach <id>`** attaches this terminal to a session, one here or a host's as `acmd list` shows it (`acmd attach $(acmd find api)`). Ctrl+] detaches. With `--host`, it picks among that host's own sessions.
- **`acmd inbox`** lists what's waiting: permission prompts, questions and finished turns, here and on your hosts. `acmd inbox answer <item> <option>` answers one, the item by its id (or `host:id`), by an option's number or text, or with a reply to a finished turn. `acmd inbox dismiss <item>` clears one.
- **`acmd watch`** prints each notice as it comes, one line each, and rings the bell (`--json`: one event per line).

While attach and watch run, they count as this machine's terminal client, so `notify.route = "one"` sends notices to the terminal you're using.

`--host <name>` (or `ACMD_HOST`) runs the command on a known Remote host instead of this Mac: `acmd --host studio list --json`. A paired machine can do on the host what you can, except admin ops (purge, init, Hosting and pairing, settings that run code) and what belongs to the host's own machine (speech, waking, hooks).

| Exit code | Meaning |
| -- | -- |
| 0 | Done |
| 1 | Failed, for any reason below doesn't cover |
| 2 | Usage: the command line doesn't make sense |
| 3 | A Remote host is unreachable |
| 4 | A Remote host is waiting for its user to approve this machine |
| 5 | A Remote host refused this machine |
| 6 | A Remote host's fingerprint isn't the one you pinned |
| 7 | A Remote host speaks another protocol version |
| 8 | Hosting is on, but can't listen on its port |
| 9 | Not found: no such session, group, job or host |
| 10 | Invalid: a bad name, value or schedule |
| 11 | Not supported: the host can't do that, or a paired machine may not |
| 12 | Not running: the session's terminal has ended |
| 13 | Partial: some of several sessions didn't change |
| 14 | Conflict: it changed while acmd was saving it; run the command again |
| 15 | A Remote host is disconnected: `acmd remote connect` connects it |

`acmd --help` lists them too.

## Install

acmd is the one program every machine needs. It runs the daemon, the sessions and the Claude Code hooks, and it's the CLI and the terminal UI. An app is a client of it, and optional. So there are two things you can install:

- **The app and acmd**, for a machine you sit at: the Mac app, or the Linux UI on a Linux desktop.
- **acmd only**, for a headless machine, one with no screen, or one you reach only from another. You use it with `acmd` and its terminal UI, or from another machine's app through [Remote sessions](#remote-sessions). On Windows it's always acmd only.

| | macOS 26 or later, Apple Silicon | Linux (amd64, arm64) and WSL2 | Windows 10 1803 or later, or 11 (amd64, arm64) |
| -- | -- | -- | -- |
| **The app and acmd** | [Homebrew cask](#the-app-and-acmd) | acmd any way below, then [build the Linux UI](#the-linux-ui) | No app |
| **acmd only, with Homebrew** | [Homebrew formula](#acmd-only) | [Homebrew formula](#acmd-only) | No |
| **acmd only, with the install script** | [`install.sh`](#acmd-only-with-the-install-script) | [`install.sh`](#acmd-only-with-the-install-script) | [`install.ps1`](#windows) |
| **acmd only, with Scoop** | No | No | [Scoop](#windows) |

**Homebrew, Scoop or the install script?** Each installs the same acmd from the same release, and each runs `acmd init`. Homebrew and Scoop upgrade it with the rest of what they manage. The install scripts need no Homebrew, no Scoop, no sudo and no admin, and upgrade acmd when you run them again. Use one of them per machine: two copies of acmd would mean two daemons, so an install script stops if acmd from Homebrew or Scoop is installed. The Mac app comes only as a cask, so it needs Homebrew.

### The app and acmd

macOS 26 or later, on Apple Silicon:

```sh
brew install --cask brettmostert/tap/agent-command
```

The cask installs the acmd formula, and runs `acmd init`, which adds the Claude Code hooks (removing any old ones first) and the service. Agent Command isn't notarized, so the cask also clears macOS's quarantine flag for it. Run `acmd` to open it (over SSH it opens the terminal UI instead).

**Upgrade:**

```sh
brew upgrade agent-command
```

Homebrew upgrades only the app: it installs a cask's formula when it's missing, but never upgrades it. So when the app next opens and finds acmd older than itself, it runs `brew upgrade brettmostert/tap/acmd`, and the status bar says so while it runs. Within a minute, the daemon restarts on the new acmd by itself, and sessions keep running. If brew fails, the app's warnings say why; then run `brew upgrade acmd` yourself. The daemon looks for a newer release every few hours, and the app's status bar shows a dot when there is one; `acmd config set updates.check off` turns that off, and then nothing is fetched.

**Uninstall.** `brew uninstall --cask agent-command` removes the app only: acmd and its sessions keep running. `--zap` also removes the app's preferences and the wake rule. To remove acmd too, follow [Uninstall acmd](#uninstall-acmd).

### acmd only

Linux (amd64 and arm64), WSL2, or macOS 26 or later on Apple Silicon:

```sh
brew install brettmostert/tap/acmd
acmd init
```

`acmd init` adds the Claude Code hooks and installs the service, which runs acmd's daemon from boot on Linux and WSL2 and from login on macOS (see [The service](#the-service)). The install tries it too, but Homebrew's sandbox may stop it, so run it once yourself: it's safe to rerun. WSL2 is Linux here, and needs systemd turned on for the service. A headless Mac needs automatic login (System Settings → Users & Groups), since a launchd agent starts at login. To use the machine from another, see [A headless host](docs/remote-sessions.md#a-headless-host). Without a screen, `acmd` opens the terminal UI.

**Upgrade:**

```sh
brew upgrade acmd
```

Within a minute, the service's daemon restarts on the new version by itself, and sessions keep running.

**Uninstall.** See [Uninstall acmd](#uninstall-acmd).

### acmd only, with the install script

Linux (amd64 and arm64), WSL2, or macOS 26 or later on Apple Silicon, with no Homebrew and no sudo:

```sh
curl -fsSL https://raw.githubusercontent.com/brettmostert/agent-command/main/scripts/install.sh | sh
```

It downloads the latest release, checks its SHA-256, and installs acmd in `~/.local/share/agent-command/<version>`, with `acmd` and `agent-command` linked in `~/.local/bin`. Then it runs `acmd init`. If [gh](https://cli.github.com) is installed and logged in, it also checks the build's attestation. If acmd from Homebrew is installed, it stops: two copies would mean two daemons, so use `brew upgrade acmd` for that one.

**Upgrade:** run it again. Within a minute, the service's daemon restarts on the new version by itself, and sessions keep running.

**Options**, as flags (`curl … | sh -s -- --no-init`) or environment variables:

| Flag | Variable | What it does |
| -- | -- | -- |
| `--version 0.96.0` | `ACMD_VERSION=0.96.0` | Installs that version, not the latest. |
| `--no-init` | `ACMD_NO_INIT=1` | Skips `acmd init`. |
| `--uninstall` | `ACMD_UNINSTALL=1` | Runs `acmd uninstall --yes`, then removes acmd and its links. |

### The Linux UI

The Linux UI is a GTK4 app for a Linux desktop. Releases don't include it yet, so build it from a clone of this repo, with the Go version `go.mod` names, a C compiler, pkg-config, and the GTK4, libadwaita and VTE (GTK4) libraries:

```sh
# Debian and Ubuntu
sudo apt-get install gcc pkg-config libgtk-4-dev libadwaita-1-dev libvte-2.91-gtk4-dev
# Arch
sudo pacman -S --needed base-devel gtk4 libadwaita vte4

git clone https://github.com/brettmostert/agent-command && cd agent-command
git checkout "v$(acmd version | cut -d' ' -f2)"   # the release of the acmd you run
make linux-ui
install -m 755 dist/agent-command-ui ~/.local/bin/
```

Install acmd first, any way above, so the checkout matches it. acmd looks for `agent-command-ui` next to itself, then on your PATH. With it there, `acmd` on a desktop opens the Linux UI, and `acmd --terminal` opens the terminal UI instead. acmd is in beta and what it sends its clients can change, so when you upgrade acmd, check out the new release and build the Linux UI again.

### The service

`acmd init` installs the service, which runs acmd's daemon as you: a launchd agent on macOS (`~/Library/LaunchAgents/com.brettmostert.agent-command.daemon.plist`), which starts it at login, and a systemd user unit on Linux and WSL2 (`~/.config/systemd/user/agent-command.service`), which starts it at boot with lingering on. Sessions keep running when it restarts. `acmd service status` says whether it's installed and running, `acmd service install` puts it back, and `acmd service uninstall` removes it; without it, the daemon starts when something needs it. WSL2 needs systemd turned on (`[boot] systemd=true` in `/etc/wsl.conf`, then `wsl --shutdown`). `acmd daemon restart` and `acmd daemon stop` go through the service.

`acmd doctor` checks the setup, and `acmd doctor --fix` re-adds the hooks and replaces a stale daemon. `acmd doctor --json` prints the daemon's health list: the same warnings the app shows, each with its fix.

### Uninstall acmd

Run `acmd uninstall` first: it ends running sessions, stops the daemon, removes its service and the hooks, and asks whether to keep your sessions and settings (`--keep-data=false` deletes them). Then remove acmd the way you installed it: `brew uninstall acmd`, after removing the app if it's installed (Homebrew has no way for a formula to run `acmd uninstall` itself). With the install script, `--uninstall` does both steps: it runs `acmd uninstall --yes`, which keeps your sessions and settings, then removes acmd. After an uninstall, Claude sessions can still be resumed, and with your data kept, terminals come back as new shells in the same folders.

### Windows

acmd on Windows (Windows 10 1803 or later, or Windows 11; amd64 and arm64), with [Scoop](https://scoop.sh):

```powershell
scoop bucket add brettmostert https://github.com/brettmostert/scoop-bucket
scoop install acmd
```

Or, with no Scoop and no admin, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/brettmostert/agent-command/main/scripts/install.ps1 | iex
```

The install script does what Scoop does: it installs acmd in `%LOCALAPPDATA%\Programs\agent-command\<version>`, points the folder `current` there, puts `current` on your PATH and runs `acmd init`. Run it again to upgrade. If acmd from Scoop is installed, it stops: use `scoop update acmd` for that one. `irm … | iex` takes no arguments, so its options are the environment variables above: for example `$env:ACMD_UNINSTALL = 1` before it removes acmd.

On Windows acmd only connects to hosts: it runs no sessions of its own, and can't host them. Pair it with a host as in [Windows](docs/remote-sessions.md#windows), then `acmd list` shows the host's sessions. The install runs `acmd init`, which adds a logon task in Task Scheduler (`Agent Command daemon`) that starts the daemon hidden at logon, as you, with no password stored. acmd keeps its files in `%LOCALAPPDATA%\agent-command`, which only you can open. `scoop update acmd` upgrades it and restarts the daemon, and `scoop uninstall acmd` removes the task. WSL2 is Linux: use brew or `install.sh` there.

<details>
<summary>Manual download</summary>

Download `AgentCommand.zip`, `acmd-<version>-darwin-arm64.tar.gz` and `checksums.txt` from the releases in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap/releases), then:

```sh
shasum -a 256 -c --ignore-missing checksums.txt
unzip AgentCommand.zip
mv "Agent Command.app" /Applications/
xattr -dr com.apple.quarantine "/Applications/Agent Command.app"
tar -xzf acmd-*-darwin-arm64.tar.gz
# Put acmd-<version>/bin and libexec side by side somewhere on your PATH, then:
acmd init
```

The app runs `/opt/homebrew/bin/acmd`, or the acmd that opens it (`acmd`).
</details>

## Development

Requires Go (version from `go.mod`), Xcode, and the Zig version Ghostty's pinned release asks for.

```sh
make help                # list targets
make ci                  # lint, test (race + coverage), vuln, build
scripts/build-ghostty.sh # build libghostty once
make app                 # build "dist/Agent Command.app"
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow and release process.

## License

[MIT](LICENSE)
