package harness

import (
	"reflect"
	"testing"
)

var (
	_ Harness  = Antigravity{}
	_ Prompter = Antigravity{}
	_ Prompter = Claude{}
)

func TestAntigravityCommand(t *testing.T) {
	a := Antigravity{Path: "/bin/agy"}
	for _, tc := range []struct {
		sp   Spec
		want []string
	}{
		{Spec{}, []string{"/bin/agy"}},
		// Name and SessionID have no agy flag.
		{Spec{Dir: "/w", Name: "fix", SessionID: "u-1"}, []string{"/bin/agy"}},
		{Spec{Prompt: "fix the tests"}, []string{"/bin/agy", "-i=fix the tests"}},
		{Spec{Prompt: "-x"}, []string{"/bin/agy", "-i=-x"}},
		{Spec{Prompt: "go", Args: []string{"--model", "flash"}}, []string{"/bin/agy", "-i=go", "--model", "flash"}},
		{Spec{Args: []string{"--remote-control"}}, []string{"/bin/agy", "--remote-control"}},
	} {
		if got := a.Command(tc.sp); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Command(%+v) = %v, want %v", tc.sp, got, tc.want)
		}
	}
}

func TestAntigravityResume(t *testing.T) {
	a := Antigravity{Path: "/bin/agy"}
	if got, want := a.Resume("conv-1", nil), []string{"/bin/agy", "--conversation", "conv-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resume = %v, want %v", got, want)
	}
	if got, want := a.Resume("conv-1", []string{"--mode", "plan"}), []string{"/bin/agy", "--conversation", "conv-1", "--mode", "plan"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resume with args = %v, want %v", got, want)
	}
}

func TestAntigravityMeta(t *testing.T) {
	a := Antigravity{}
	if a.Name() != "antigravity" || a.StatusSource() != StatusHooks {
		t.Error("unexpected metadata")
	}
	if got := (Claude{}).PromptArgs("-x"); !reflect.DeepEqual(got, []string{"--", "-x"}) {
		t.Errorf("Claude.PromptArgs = %v", got)
	}
}
