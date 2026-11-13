package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/wake"
)

// wakeNote is what waking can't do, said wherever it's turned on.
const wakeNote = "A MacBook with its lid closed on battery may not wake, or may sleep again before the run: " +
	"on power, or with the lid open, it does."

// localWaker reads and checks this Mac's wakes; swappable in tests, so they
// never touch the real power schedule.
var localWaker = func() wake.Waker { return wake.NewMac() }

// asRoot runs a shell script as root: with sudo in a terminal, else through
// macOS's admin password dialog. Swappable in tests.
var asRoot = func(ctx context.Context, in io.Reader, out io.Writer, script, prompt string) error {
	if f, ok := in.(*os.File); ok && isTerminal(f) {
		fmt.Fprintln(out, prompt)
		cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "/bin/sh", "-c", script) //nolint:gosec // G204: a script internal/wake writes, run where the person types their password
		cmd.Stdin, cmd.Stdout, cmd.Stderr = f, out, os.Stderr
		return cmd.Run()
	}
	b, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", wake.AppleScript(script, prompt)).CombinedOutput() //nolint:gosec // G204: as above, through the admin dialog
	if err != nil {
		if strings.Contains(string(b), "-128") {
			return errors.New("canceled")
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// ruleInstalled says whether acmd's sudoers rule is there (stat needs no root).
var ruleInstalled = func() bool {
	_, err := os.Stat(wake.RulePath)
	return err == nil
}

func newScheduleWakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Wake the Mac for scheduled jobs",
		Long: `Wake the Mac shortly before a job's run, so it runs on time.

"acmd schedule wake on" asks for your password once, to add a sudoers rule
(` + wake.RulePath + `) that lets you set and cancel acmd's own wakes, and
nothing else, without one. Then every job wakes the Mac with --all, or the jobs
added or edited with --wake. "acmd schedule wake off" clears acmd's wakes and
removes the rule.

` + wakeNote,
	}
	cmd.AddCommand(newWakeStatusCmd(), newWakeOnCmd(), newWakeOffCmd())
	return cmd
}

func newWakeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Say whether acmd may wake the Mac, for which jobs, and when next",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				writeWake(cmd.OutOrStdout(), snap)
				return nil
			})
		},
	}
}

func writeWake(out io.Writer, snap ipc.Snapshot) {
	w := snap.Wake
	if w == nil {
		fmt.Fprintln(out, "This acmd can't wake the Mac.")
		return
	}
	if !w.Allowed {
		fmt.Fprintln(out, "Waking the Mac is off. Turn it on with `acmd schedule wake on`.")
		return
	}
	fmt.Fprintln(out, "Waking the Mac is on.")
	var names []string
	for _, j := range snap.Jobs {
		if wantsWake(j.Paused, j.Wake, w.All) {
			names = append(names, fmt.Sprintf("%q", j.Name))
		}
	}
	switch {
	case w.All:
		fmt.Fprintln(out, "Every job wakes it.")
	case len(names) == 0:
		fmt.Fprintln(out, "No job wakes it yet: add --wake to one, or turn it on for every job with `acmd schedule wake on --all`.")
	default:
		fmt.Fprintf(out, "Jobs that wake it: %s.\n", strings.Join(names, ", "))
	}
	if !w.Next.IsZero() {
		fmt.Fprintf(out, "Next wake: %s.\n", whenText(w.Next))
	}
	if w.Error != "" {
		fmt.Fprintf(out, "The last try failed: %s\n", w.Error)
	}
	fmt.Fprintln(out, wakeNote)
}

func wantsWake(paused, job, all bool) bool { return !paused && (all || job) }

func newWakeOnCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "on",
		Short: "Let acmd wake the Mac (asks for your password once)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if !localWaker().Allowed(ctx) {
				u, err := user.Current()
				if err != nil {
					return err
				}
				script, err := wake.InstallScript(u.Username)
				if err != nil {
					return err
				}
				prompt := "Agent Command wants to wake the Mac for scheduled jobs. This adds " + wake.RulePath + "."
				if err := asRoot(ctx, cmd.InOrStdin(), out, script, prompt); err != nil {
					return fmt.Errorf("adding the rule: %w", err)
				}
				if !localWaker().Allowed(ctx) {
					return errors.New("the rule is in place, but sudo still asks for a password: see `sudo -l`")
				}
			}
			return withClient(ctx, func(c *client, snap ipc.Snapshot) error {
				ref := ipc.WakeRef{All: all}
				if !cmd.Flags().Changed("all") && snap.Wake != nil {
					ref.All = snap.Wake.All
				}
				var st ipc.WakeState
				if err := c.conn.Call(ipc.OpWakeSet, ref, &st); err != nil {
					return err
				}
				snap.Wake = &st
				writeWake(out, snap)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "wake for every job, not only those with --wake (--all=false: only those)")
	return cmd
}

func newWakeOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Clear acmd's wakes and remove its rule (asks for your password)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := removeWakeRule(ctx, cmd.InOrStdin(), cmd.OutOrStdout()); err != nil {
				return err
			}
			return withClient(ctx, func(c *client, _ ipc.Snapshot) error {
				if err := c.conn.Call(ipc.OpWakeSet, ipc.WakeRef{}, nil); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Waking the Mac is off.")
				return nil
			})
		},
	}
}

// removeWakeRule cancels acmd's wakes and removes its rule, as root, in one go
// (nothing to do if neither is there).
func removeWakeRule(ctx context.Context, in io.Reader, out io.Writer) error {
	wakes, err := localWaker().Scheduled(ctx)
	if err != nil {
		return err
	}
	if len(wakes) == 0 && !ruleInstalled() {
		return nil
	}
	script := wake.CancelScript(wakes) + wake.RemoveScript()
	prompt := "Agent Command wants to stop waking the Mac. This removes " + wake.RulePath + "."
	if err := asRoot(ctx, in, out, script, prompt); err != nil {
		return fmt.Errorf("removing the rule: %w", err)
	}
	return nil
}

// wakeFlag reads the --wake flag of add and edit.
func wakeFlag(cmd *cobra.Command, val bool, current *bool) {
	if cmd.Flags().Changed("wake") {
		*current = val
	}
}

// wakeHint says, after saving a job that wakes the Mac, if waking is off.
func wakeHint(out io.Writer, snap ipc.Snapshot, wakes bool) {
	if wakes && (snap.Wake == nil || !snap.Wake.Allowed) {
		fmt.Fprintln(out, "It can't wake the Mac yet: turn that on with `acmd schedule wake on`.")
	}
}
