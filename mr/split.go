package mr

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Split is the byte range of an input file handed to a single map task.
type Split struct {
	File   string
	Offset int64
	Length int64
}

// PlanSplits divides the input files into splits of about splitBytes each.
// A splitBytes of zero or less gives each whole file its own split.
func PlanSplits(files []string, splitBytes int) ([]Split, error) {
	splits := []Split{}

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return nil, fmt.Errorf("stat %v: %w", file, err)
		}
		size := info.Size()

		if splitBytes <= 0 || size <= int64(splitBytes) {
			splits = append(splits, Split{File: file, Offset: 0, Length: size})
			continue
		}

		for offset := int64(0); offset < size; offset += int64(splitBytes) {
			length := int64(splitBytes)
			if offset+length > size {
				length = size - offset
			}
			splits = append(splits, Split{File: file, Offset: offset, Length: length})
		}
	}

	return splits, nil
}

// ReadSplit returns the split's contents rounded out to whole lines. A split
// that starts mid file leaves its first partial line to the split before it,
// and every split runs on to the end of the line crossing its last byte, so
// concatenating all splits of a file reproduces the file exactly once.
func ReadSplit(s Split) (string, error) {
	file, err := os.Open(s.File)
	if err != nil {
		return "", fmt.Errorf("open %v: %w", s.File, err)
	}
	defer file.Close()

	// A split beginning right after a newline already starts on a whole line;
	// otherwise its first line is partial and belongs to the split before it.
	atLineStart := s.Offset == 0
	if s.Offset > 0 {
		if _, err := file.Seek(s.Offset-1, io.SeekStart); err != nil {
			return "", fmt.Errorf("seek %v: %w", s.File, err)
		}
		var prev [1]byte
		if _, err := io.ReadFull(file, prev[:]); err != nil {
			return "", fmt.Errorf("read %v: %w", s.File, err)
		}
		atLineStart = prev[0] == '\n'
	}

	reader := bufio.NewReader(file)
	pos := s.Offset

	if !atLineStart {
		skipped, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read %v: %w", s.File, err)
		}
		pos += int64(len(skipped))
	}

	end := s.Offset + s.Length
	var content strings.Builder
	for pos < end {
		line, err := reader.ReadBytes('\n')
		content.Write(line)
		pos += int64(len(line))
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("read %v: %w", s.File, err)
		}
	}

	return content.String(), nil
}
