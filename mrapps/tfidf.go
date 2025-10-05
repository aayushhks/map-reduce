package main

// a tf-idf application plugin for MapReduce.

import (
	"cs651/mr"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"unicode"
)

// A helper struct for the final Reduce output format.
// The default string representation of a slice of these structs
// matches the required output format, e.g., "[{doc1 123} {doc2 456}]".

type DocResult struct {
	DocName string
	Tfidf   int
}

// The map function is called once for each file of input. It is responsible
// for calculating the term frequency (TF) component for each word.

func Map(filename string, contents string) []mr.KeyValue {
	// 1. Preprocessing: Function to split text into words (handles punctuation).
	ff := func(r rune) bool {
		return !unicode.IsLetter(r)
	}
	words := strings.FieldsFunc(strings.ToLower(contents), ff)

	// 2. Calculate document length (dl) and term counts (tc).
	docLen := len(words)
	wordCounts := make(map[string]int)
	for _, word := range words {
		wordCounts[word]++
	}

	// 3. Emit key-value pairs.
	// Key: a word
	// Value: "document_name,term_count,document_length"
	kva := []mr.KeyValue{}
	basename := path.Base(filename) // Use just the filename, not the full path.
	for word, count := range wordCounts {
		// This value bundles all the info needed for the TF calculation later.
		value := fmt.Sprintf("%s,%d,%d", basename, count, docLen)
		kva = append(kva, mr.KeyValue{Key: word, Value: value})
	}
	return kva
}

// The reduce function is called once for each key (word). It receives all
// the per-document information from the mappers and calculates the final
// modified TF-IDF score.

func Reduce(key string, values []string) string {
	// The total number of documents is a constant for this assignment.
	const numDocs = 8.0

	// 1. The document count (dc) for this term is the number of values we received.
	docCountWithTerm := len(values)

	// 2. Calculate the Inverse Document Frequency (IDF) component.
	// This is the same for all documents for this specific word.
	idf := math.Log10(1.0 + numDocs/(1.0+float64(docCountWithTerm)))

	// 3. Iterate through documents, calculate TF, and compute final TF-IDF.
	results := []DocResult{}
	for _, val := range values {
		parts := strings.Split(val, ",")
		docName := parts[0]

		// It's safe to ignore errors here as we control the format.
		tc, _ := strconv.Atoi(parts[1]) // term count
		dl, _ := strconv.Atoi(parts[2]) // document length

		// Calculate Term Frequency (TF).
		tf := float64(tc) / float64(dl)

		// Calculate and scale the final score as per the instructions.
		score := int(math.Round(1e7 * tf * idf))

		results = append(results, DocResult{DocName: docName, Tfidf: score})
	}

	// 4. Format the final output string.
	// The default Go format for the slice of structs matches the requirement.
	return fmt.Sprintf("%v", results)
}
