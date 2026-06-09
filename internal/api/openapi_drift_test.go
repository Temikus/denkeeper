package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestOpenAPISpecMatchesAnnotations regenerates the OpenAPI spec from the swag
// annotations and fails if it diverges from the committed
// internal/api/docs/swagger.json. This guards against the generated spec drifting
// out of sync with the handler @-annotations — run `just openapi` to regenerate
// and commit the result after changing any annotation.
//
// swag emits no volatile metadata into swagger.json (object keys are sorted and
// there is no embedded timestamp), so a byte-for-byte comparison is stable; the
// generation timestamp only lives in docs.go, which `just openapi` deletes.
func TestOpenAPISpecMatchesAnnotations(t *testing.T) {
	swag, err := exec.LookPath("swag")
	if err != nil {
		// In CI swag is provided by mise (.mise.toml); a missing binary there is
		// a real problem, not a reason to silently skip the drift gate.
		if os.Getenv("CI") != "" {
			t.Fatal("swag not found on PATH in CI — it must be installed via mise (.mise.toml)")
		}
		t.Skip("swag not on PATH; run through `just test` (mise provides swag) to enable this check")
	}

	_, thisFile, _, _ := runtime.Caller(0)
	apiDir := filepath.Dir(thisFile) // internal/api
	repoRoot := filepath.Join(apiDir, "..", "..")
	committedPath := filepath.Join(apiDir, "docs", "swagger.json")

	outDir := t.TempDir()
	cmd := exec.Command(swag, "init",
		"-g", "internal/api/server.go",
		"-o", outDir,
		"--parseDependency", "--parseInternal",
	)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("swag init failed: %v\n%s", err, out)
	}

	got, err := os.ReadFile(filepath.Join(outDir, "swagger.json"))
	if err != nil {
		t.Fatalf("reading regenerated spec: %v", err)
	}
	want, err := os.ReadFile(committedPath)
	if err != nil {
		t.Fatalf("reading committed spec %s: %v", committedPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("OpenAPI spec is out of date with the swag annotations — run `just openapi` and commit internal/api/docs/swagger.json")
	}
}
