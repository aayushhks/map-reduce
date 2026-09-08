package workload

import (
	"strings"
	"testing"
)

// The inverted index has to survive one document arriving as several splits,
// which is what lets the split size change without changing the output.
func TestInvertedIndexReduceDropsDuplicateDocuments(t *testing.T) {
	// The same document reported twice, as two splits of it would.
	got := InvertedIndexReduce("whale", []string{"moby.txt", "ahab.txt", "moby.txt"})

	if want := "2 ahab.txt,moby.txt"; got != want {
		t.Fatalf("reduce gave %q, want %q", got, want)
	}
}

// Map reports each distinct word once per split, lowercased, and names the
// document by its base name rather than the path it was read from.
func TestInvertedIndexMapEmitsDistinctWords(t *testing.T) {
	kva := InvertedIndexMap("/corpus/deep/moby.txt", "Whale whale WHALE sea")

	if len(kva) != 2 {
		t.Fatalf("emitted %d pairs, want 2: %v", len(kva), kva)
	}
	words := []string{}
	for _, kv := range kva {
		if kv.Value != "moby.txt" {
			t.Fatalf("document named %q, want moby.txt", kv.Value)
		}
		words = append(words, kv.Key)
	}
	joined := strings.Join(words, ",")
	if !strings.Contains(joined, "whale") || !strings.Contains(joined, "sea") {
		t.Fatalf("emitted %v, want whale and sea", words)
	}
}

func TestWordCountReduceCountsOccurrences(t *testing.T) {
	if got := WordCountReduce("whale", []string{"1", "1", "1"}); got != "3" {
		t.Fatalf("reduce gave %q, want 3", got)
	}
}

func TestLookupRejectsAnUnknownWorkload(t *testing.T) {
	if _, err := Lookup("nonesuch"); err == nil {
		t.Fatal("Lookup accepted an unknown workload")
	}
	if _, err := Lookup("invertedindex"); err != nil {
		t.Fatalf("Lookup(invertedindex): %v", err)
	}
}
