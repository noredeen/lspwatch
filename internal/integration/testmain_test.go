package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Integration tests historically relied on `make integration-tests`, which sets
// env vars and builds helper binaries into a ./build directory. When running
// `go test ./...` from a clean checkout, those prerequisites are missing.
//
// This TestMain makes the integration tests hermetic by:
// - Building a coverage-instrumented lspwatch binary and exporting LSPWATCH_BIN
// - Creating a writable coverage directory and exporting COVERAGE_DIR
// - Building helper servers and exporting INTEGRATION_BUILD_DIR
// - Defaulting TEST_DATA_DIR to ./testdata
func TestMain(m *testing.M) {
	packageDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration TestMain: failed to get wd:", err)
		os.Exit(2)
	}

	// Default TEST_DATA_DIR for local `go test ./...`.
	if os.Getenv("TEST_DATA_DIR") == "" {
		_ = os.Setenv("TEST_DATA_DIR", filepath.Join(packageDir, "testdata"))
	}

	tmpRoot, err := os.MkdirTemp("", "lspwatch-integration-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration TestMain: failed to create temp dir:", err)
		os.Exit(2)
	}
	defer os.RemoveAll(tmpRoot)

	// Ensure COVERAGE_DIR exists for subprocess coverage export.
	if os.Getenv("COVERAGE_DIR") == "" {
		coverageDir := filepath.Join(tmpRoot, "coverage")
		if err := os.MkdirAll(coverageDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "integration TestMain: failed to create coverage dir:", err)
			os.Exit(2)
		}
		_ = os.Setenv("COVERAGE_DIR", coverageDir)
	}

	// Build helper binaries into tmpRoot and expose path.
	_ = os.Setenv("INTEGRATION_BUILD_DIR", tmpRoot)

	repoRoot := filepath.Clean(filepath.Join(packageDir, "..", ".."))

	// If a binary is already provided (e.g. via Makefile), respect it.
	if os.Getenv("LSPWATCH_BIN") == "" {
		lspwatchBin := filepath.Join(tmpRoot, "lspwatch_cov")
		cmd := exec.Command("go", "build", "-cover", "-covermode=atomic", "-o", lspwatchBin, "./")
		cmd.Dir = repoRoot
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintln(os.Stderr, "integration TestMain: failed to build lspwatch:", err)
			fmt.Fprintln(os.Stderr, string(out))
			os.Exit(2)
		}
		_ = os.Setenv("LSPWATCH_BIN", lspwatchBin)
	}

	// Build integration helper servers if they don't exist.
	// `go build -C <pkgdir> -o <dir>/ ./cmd/...` yields binaries named after each cmd.
	cmd := exec.Command("go", "build", "-C", packageDir, "-o", tmpRoot+string(os.PathSeparator), "./cmd/...")
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration TestMain: failed to build integration helpers:", err)
		fmt.Fprintln(os.Stderr, string(out))
		os.Exit(2)
	}

	os.Exit(m.Run())
}

