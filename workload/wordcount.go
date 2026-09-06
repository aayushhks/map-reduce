package workload

import (
	"strconv"
	"strings"
	"unicode"

	"cs651/mr"
)

// WordCountMap emits one pair per word occurrence.
func WordCountMap(filename string, contents string) []mr.KeyValue {
	words := strings.FieldsFunc(contents, func(r rune) bool {
		return !unicode.IsLetter(r)
	})

	kva := make([]mr.KeyValue, 0, len(words))
	for _, w := range words {
		kva = append(kva, mr.KeyValue{Key: w, Value: "1"})
	}
	return kva
}

// WordCountReduce counts the occurrences of one word.
func WordCountReduce(key string, values []string) string {
	return strconv.Itoa(len(values))
}
