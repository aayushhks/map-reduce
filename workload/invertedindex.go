package workload

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"cs651/mr"
)

// InvertedIndexMap emits one pair per distinct word in this piece of input.
// Duplicates across splits of the same document are removed by the reduce step,
// so the job's output does not depend on how the input was split.
func InvertedIndexMap(filename string, contents string) []mr.KeyValue {
	document := filepath.Base(filename)

	seen := make(map[string]struct{})
	for _, word := range strings.FieldsFunc(contents, func(r rune) bool {
		return !unicode.IsLetter(r)
	}) {
		seen[strings.ToLower(word)] = struct{}{}
	}

	kva := make([]mr.KeyValue, 0, len(seen))
	for word := range seen {
		kva = append(kva, mr.KeyValue{Key: word, Value: document})
	}
	return kva
}

// InvertedIndexReduce lists the distinct documents containing a word, so the
// same word reported by several splits of one document counts once.
func InvertedIndexReduce(key string, values []string) string {
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		seen[v] = struct{}{}
	}

	documents := make([]string, 0, len(seen))
	for d := range seen {
		documents = append(documents, d)
	}
	sort.Strings(documents)

	return fmt.Sprintf("%d %s", len(documents), strings.Join(documents, ","))
}
