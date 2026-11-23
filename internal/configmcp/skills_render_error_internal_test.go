package configmcp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/skill"
)

const refusalPrefix = "the edit was not saved: denkeeper could not rewrite this skill's frontmatter " +
	"without changing keys it does not read. Edit "

var errUnknownKeysGuard = fmt.Errorf("%w (before [license], after [])", skill.ErrUnknownKeysChanged)

// No known input trips the unknown-key guard, so its wording is tested here.
func TestExplainRenderError_FlatSkillNamesTheSkillsDirectory(t *testing.T) {
	err := explainRenderError(errUnknownKeysGuard, skill.Skill{Name: "tidy"})
	want := refusalPrefix + "the skill's file in the agent's skills directory by hand ("
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want it to start %q", err, want)
	}
	if !errors.Is(err, skill.ErrUnknownKeysChanged) || !strings.Contains(err.Error(), "before [license]") {
		t.Errorf("err = %v, want the guard's detail kept", err)
	}
}

func TestExplainRenderError_DirectorySkillNamesItsFile(t *testing.T) {
	dir := filepath.Join("skills", "kelos")
	err := explainRenderError(errUnknownKeysGuard, skill.Skill{Name: "kelos", Dir: dir})
	want := refusalPrefix + filepath.Join(dir, "SKILL.md") + " by hand ("
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want it to start %q", err, want)
	}
}

func TestExplainRenderError_OtherErrorsUnchanged(t *testing.T) {
	other := errors.New("skill: name is required")
	if got := explainRenderError(other, skill.Skill{}); got != other {
		t.Errorf("got %v, want the error unchanged", got)
	}
	if explainRenderError(nil, skill.Skill{}) != nil {
		t.Error("nil error was wrapped")
	}
}
