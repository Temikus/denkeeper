package configmcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/skill"
)

// No known input trips the unknown-key guard, so its wording is tested here.
func TestExplainRenderError_UnknownKeysChanged(t *testing.T) {
	guard := fmt.Errorf("%w (before [license], after [])", skill.ErrUnknownKeysChanged)
	err := explainRenderError(guard)
	want := "the edit was not saved: denkeeper could not rewrite this skill's frontmatter " +
		"without changing keys it does not read. Edit the file by hand ("
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want it to start %q", err, want)
	}
	if !errors.Is(err, skill.ErrUnknownKeysChanged) || !strings.Contains(err.Error(), "before [license]") {
		t.Errorf("err = %v, want the guard's detail kept", err)
	}
}

func TestExplainRenderError_OtherErrorsUnchanged(t *testing.T) {
	other := errors.New("skill: name is required")
	if got := explainRenderError(other); got != other {
		t.Errorf("got %v, want the error unchanged", got)
	}
	if explainRenderError(nil) != nil {
		t.Error("nil error was wrapped")
	}
}
