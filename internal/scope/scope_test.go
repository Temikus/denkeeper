package scope_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/scope"
)

// repoRoot returns the repository root by walking up from this test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// file is …/internal/scope/scope_test.go → go up 3 levels
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestScopeCanonicalList ensures the canonical list is non-empty and sorted
// helper Names() works.
func TestScopeCanonicalList(t *testing.T) {
	names := scope.Names()
	if len(names) == 0 {
		t.Fatal("scope.Names() returned empty list")
	}
	for i := 1; i < len(names); i++ {
		if names[i] <= names[i-1] {
			t.Errorf("scope.Names() not sorted: %q comes after %q", names[i], names[i-1])
		}
	}
}

// TestFrontendApiKeysScopesInSync verifies that web/src/pages/ApiKeys.svelte
// contains every scope from the canonical list.
func TestFrontendApiKeysScopesInSync(t *testing.T) {
	path := filepath.Join(repoRoot(t), "web", "src", "pages", "ApiKeys.svelte")
	assertFileContainsAllScopes(t, path)
}

// TestFrontendLoginScopesInSync verifies that web/src/pages/Login.svelte
// contains every scope from the canonical list.
func TestFrontendLoginScopesInSync(t *testing.T) {
	path := filepath.Join(repoRoot(t), "web", "src", "pages", "Login.svelte")
	assertFileContainsAllScopes(t, path)
}

// TestCLIKeysUsesCanonicalScopes verifies that cmd/denkeeper/keys.go
// imports the scope package and uses scope.Names() rather than a hardcoded
// scope list. This ensures the CLI help text stays in sync automatically.
func TestCLIKeysUsesCanonicalScopes(t *testing.T) {
	path := filepath.Join(repoRoot(t), "cmd", "denkeeper", "keys.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	content := string(data)
	if !strings.Contains(content, `"github.com/Temikus/denkeeper/internal/scope"`) {
		t.Error("keys.go does not import the scope package — help text may be out of sync")
	}
	if !strings.Contains(content, "scope.Names()") {
		t.Error("keys.go does not use scope.Names() — help text may use a hardcoded scope list")
	}
}

func assertFileContainsAllScopes(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	content := string(data)

	for _, s := range scope.Names() {
		if !strings.Contains(content, s) {
			t.Errorf("scope %q missing from %s", s, filepath.Base(path))
		}
	}
}
