package verify

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %v: %v", name, err)
	}
}

// A worker killed mid task leaves temp files behind. They must never be read
// as job output, which once made a fault scenario report a wrong hash.
func TestOutputHashIgnoresTempFileDebris(t *testing.T) {
	clean := t.TempDir()
	write(t, clean, "mr-out-0", "alpha 1\nbeta 2\n")
	write(t, clean, "mr-out-1", "gamma 3\n")

	want, keys, err := OutputHash(clean)
	if err != nil {
		t.Fatalf("hash clean dir: %v", err)
	}
	if keys != 3 {
		t.Fatalf("counted %d output lines, want 3", keys)
	}

	littered := t.TempDir()
	write(t, littered, "mr-out-0", "alpha 1\nbeta 2\n")
	write(t, littered, "mr-out-1", "gamma 3\n")
	// Debris a killed worker can leave: an abandoned reduce temp file, an
	// abandoned map temp file, and an intermediate partition.
	write(t, littered, ".mrtmp-out-1-2891479862", "gamma 3\n")
	write(t, littered, ".mrtmp-map-0-1-118273", "{\"Key\":\"x\"}\n")
	write(t, littered, "mr-0-1", "{\"Key\":\"x\"}\n")

	got, keys, err := OutputHash(littered)
	if err != nil {
		t.Fatalf("hash littered dir: %v", err)
	}
	if keys != 3 {
		t.Fatalf("counted %d output lines with debris present, want 3", keys)
	}
	if got != want {
		t.Fatalf("debris changed the hash\n got  %s\n want %s", got, want)
	}
}

// The digest must depend on the job's results, not on which reduce task
// produced which key.
func TestOutputHashIsIndependentOfPartitioning(t *testing.T) {
	a := t.TempDir()
	write(t, a, "mr-out-0", "alpha 1\nbeta 2\ngamma 3\n")

	b := t.TempDir()
	write(t, b, "mr-out-0", "gamma 3\n")
	write(t, b, "mr-out-1", "alpha 1\n")
	write(t, b, "mr-out-2", "beta 2\n")

	ha, _, err := OutputHash(a)
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	hb, _, err := OutputHash(b)
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if ha != hb {
		t.Fatalf("partitioning changed the hash\n one file  %s\n three     %s", ha, hb)
	}
}

// A changed result must change the digest, or the check is worthless.
func TestOutputHashDetectsAChangedValue(t *testing.T) {
	a := t.TempDir()
	write(t, a, "mr-out-0", "alpha 1\nbeta 2\n")
	b := t.TempDir()
	write(t, b, "mr-out-0", "alpha 1\nbeta 3\n")

	ha, _, _ := OutputHash(a)
	hb, _, _ := OutputHash(b)
	if ha == hb {
		t.Fatal("a changed value produced the same hash")
	}
}

func TestOutputHashOnAnEmptyDirectory(t *testing.T) {
	h, keys, err := OutputHash(t.TempDir())
	if err != nil {
		t.Fatalf("hash empty dir: %v", err)
	}
	if keys != 0 || h == "" {
		t.Fatalf("empty dir gave keys=%d hash=%q", keys, h)
	}
}
