package mr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCorpus(t *testing.T, lines int) (string, string) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < lines; i++ {
		b.WriteString(strings.Repeat("word ", 1+i%7))
		b.WriteString("\n")
	}
	content := b.String()

	path := filepath.Join(t.TempDir(), "corpus.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	return path, content
}

// Whatever the split size, the splits of a file must reassemble into exactly
// that file: no bytes dropped at a boundary and none counted twice.
func TestSplitsReassembleTheInput(t *testing.T) {
	path, content := writeCorpus(t, 500)

	for _, splitBytes := range []int{0, 1, 7, 64, 512, 4096, 1 << 20} {
		splits, err := PlanSplits([]string{path}, splitBytes)
		if err != nil {
			t.Fatalf("PlanSplits(%d): %v", splitBytes, err)
		}

		var got strings.Builder
		for _, s := range splits {
			part, err := ReadSplit(s)
			if err != nil {
				t.Fatalf("ReadSplit(%d): %v", splitBytes, err)
			}
			got.WriteString(part)
		}

		if got.String() != content {
			t.Fatalf("splitBytes=%d reassembled %d bytes, want %d",
				splitBytes, got.Len(), len(content))
		}
	}
}

// A split size larger than the file, and a zero split size, both mean one
// split per file.
func TestSplitCountsFollowSplitSize(t *testing.T) {
	path, content := writeCorpus(t, 200)
	size := len(content)

	for _, tc := range []struct {
		splitBytes int
		want       int
	}{
		{0, 1},
		{size * 2, 1},
		{size, 1},
		{size/4 + 1, 4},
	} {
		splits, err := PlanSplits([]string{path}, tc.splitBytes)
		if err != nil {
			t.Fatalf("PlanSplits(%d): %v", tc.splitBytes, err)
		}
		if len(splits) != tc.want {
			t.Fatalf("splitBytes=%d gave %d splits, want %d", tc.splitBytes, len(splits), tc.want)
		}
	}
}

func TestPlanSplitsReportsMissingFile(t *testing.T) {
	if _, err := PlanSplits([]string{filepath.Join(t.TempDir(), "absent.txt")}, 0); err == nil {
		t.Fatal("PlanSplits accepted a missing file")
	}
}
