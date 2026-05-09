package skill

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateSubpath validates and resolves a relative file path within a skill
// directory. It ensures the path is under an allowed subdirectory and cannot
// escape the skill directory.
func ValidateSubpath(skillDir, filePath string) (string, error) {
	if filepath.IsAbs(filePath) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", filePath)
	}
	if strings.Contains(filePath, "..") {
		return "", fmt.Errorf("path traversal is not allowed: %q", filePath)
	}

	parts := strings.SplitN(filepath.ToSlash(filePath), "/", 2)
	if len(parts) < 2 || parts[1] == "" {
		return "", fmt.Errorf("path must be under a subdirectory (references/, templates/, or scripts/): %q", filePath)
	}

	allowed := false
	for _, d := range AllowedSubdirs {
		if parts[0] == d {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("unknown subdirectory %q; allowed: %s", parts[0], strings.Join(AllowedSubdirs, ", "))
	}

	resolved := filepath.Join(skillDir, filepath.Clean(filePath))

	if !strings.HasPrefix(resolved, filepath.Clean(skillDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved path escapes skill directory: %q", filePath)
	}

	return resolved, nil
}
