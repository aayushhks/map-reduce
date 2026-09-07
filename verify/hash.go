package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// outputName matches a finished reduce output file and nothing else, so debris
// left by a worker that died mid task is never counted as job output.
var outputName = regexp.MustCompile(`^mr-out-[0-9]+$`)

// OutputHash digests every output line in a job's work directory, sorted so the
// digest does not depend on which reduce task produced which key. It returns
// the hash and the number of output lines.
func OutputHash(dir string) (string, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, fmt.Errorf("list output: %w", err)
	}

	paths := []string{}
	for _, e := range entries {
		if !e.IsDir() && outputName.MatchString(e.Name()) {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)

	lines := []string{}
	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			return "", 0, fmt.Errorf("read output %v: %w", p, err)
		}
		for _, line := range strings.Split(string(content), "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	sort.Strings(lines)

	sum := sha256.New()
	for _, line := range lines {
		sum.Write([]byte(line))
		sum.Write([]byte{'\n'})
	}

	return hex.EncodeToString(sum.Sum(nil)), len(lines), nil
}
