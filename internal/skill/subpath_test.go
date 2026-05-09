package skill

import (
	"strings"
	"testing"
)

func TestValidateSubpath_AllowedPrefixes(t *testing.T) {
	for _, prefix := range AllowedSubdirs {
		path := prefix + "/test.md"
		resolved, err := ValidateSubpath("/skills/greet", path)
		if err != nil {
			t.Errorf("prefix %q should be allowed, got error: %v", prefix, err)
		}
		if !strings.HasPrefix(resolved, "/skills/greet/"+prefix) {
			t.Errorf("resolved path %q should start with skill dir + prefix", resolved)
		}
	}
}

func TestValidateSubpath_TraversalRejected(t *testing.T) {
	_, err := ValidateSubpath("/skills/greet", "references/../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected traversal error, got: %v", err)
	}
}

func TestValidateSubpath_AbsolutePathRejected(t *testing.T) {
	_, err := ValidateSubpath("/skills/greet", "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("expected absolute path error, got: %v", err)
	}
}

func TestValidateSubpath_UnknownPrefixRejected(t *testing.T) {
	_, err := ValidateSubpath("/skills/greet", "secrets/api-key.txt")
	if err == nil {
		t.Fatal("expected error for unknown prefix")
	}
	if !strings.Contains(err.Error(), "unknown subdirectory") {
		t.Errorf("expected unknown subdirectory error, got: %v", err)
	}
}

func TestValidateSubpath_NoSubdirectory(t *testing.T) {
	_, err := ValidateSubpath("/skills/greet", "just-a-file.md")
	if err == nil {
		t.Fatal("expected error for file without subdirectory")
	}
}

func TestValidateSubpath_EmptyFilename(t *testing.T) {
	_, err := ValidateSubpath("/skills/greet", "references/")
	if err == nil {
		t.Fatal("expected error for empty filename after subdirectory")
	}
}
