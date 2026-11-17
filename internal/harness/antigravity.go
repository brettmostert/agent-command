package harness

// Antigravity hosts Google's Antigravity CLI (agy), interactive. agy picks
// its own conversation id, which acmd learns from its first hook.
type Antigravity struct {
	Path string // absolute path to agy
}

// Name implements Harness.
func (Antigravity) Name() string { return "antigravity" }

// Command implements Harness: `agy [-i=prompt] [args]`. agy has no flag for a
// name or a conversation id, so Spec's are unused.
func (a Antigravity) Command(sp Spec) []string {
	argv := []string{a.Path}
	if sp.Prompt != "" {
		argv = append(argv, a.PromptArgs(sp.Prompt)...)
	}
	return append(argv, sp.Args...)
}

// Resume implements Harness: `agy --conversation <id> [args]`.
func (a Antigravity) Resume(sessionID string, args []string) []string {
	return append([]string{a.Path, "--conversation", sessionID}, args...)
}

// PromptArgs implements Prompter. The value is joined to the flag: agy reads
// `-i -x` as two flags, so a prompt that starts with a dash would fail.
func (Antigravity) PromptArgs(prompt string) []string { return []string{"-i=" + prompt} }

// StatusSource implements Harness: agy calls acmd's hooks.
func (Antigravity) StatusSource() StatusSource { return StatusHooks }
