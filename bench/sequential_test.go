package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"cs651/verify"
)

// The distributed pipeline must agree with mrsequential, the single process
// reference implementation, on the committed corpus. Both sides are compared
// against the same golden constant, so the constant cannot quietly stop
// describing the reference implementation.
func TestSequentialReferenceMatchesGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a plugin and runs another binary")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("go plugins are only supported on linux and darwin")
	}

	dir := t.TempDir()
	plugin := filepath.Join(dir, "wc.so")
	binary := filepath.Join(dir, "mrsequential")

	build(t, "-buildmode=plugin", "-o", plugin, "../mrapps/wc.go")
	build(t, "-o", binary, "../mr-main/mrsequential.go")

	inputs, err := filepath.Glob("../data/*.txt")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("committed corpus not found: %v", err)
	}
	// mrsequential writes into its working directory, so it runs somewhere
	// else and needs the inputs named absolutely.
	for i, in := range inputs {
		if inputs[i], err = filepath.Abs(in); err != nil {
			t.Fatalf("resolve %v: %v", in, err)
		}
	}

	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}

	cmd := exec.Command(binary, append([]string{plugin}, inputs...)...)
	cmd.Dir = out
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mrsequential: %v\n%s", err, combined)
	}

	hash, keys, err := verify.OutputHash(out)
	if err != nil {
		t.Fatalf("hash sequential output: %v", err)
	}

	if keys != goldenWordCountKeys {
		t.Errorf("mrsequential produced %d keys, want %d", keys, goldenWordCountKeys)
	}
	if hash != goldenWordCount {
		t.Fatalf("mrsequential output hash\n got  %s\n want %s", hash, goldenWordCount)
	}
}

// build compiles a helper binary, failing the test with the compiler output.
func build(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"build"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %v: %v\n%s", args, err, out)
	}
}
