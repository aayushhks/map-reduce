package workload

import (
	"fmt"
	"sort"

	"cs651/mr"
)

// Workload is a named map and reduce pair a benchmark can run.
type Workload struct {
	Name   string
	Map    func(string, string) []mr.KeyValue
	Reduce func(string, []string) string
}

// registry holds every workload the benchmark harness can select by name.
var registry = map[string]Workload{
	"wordcount": {
		Name:   "wordcount",
		Map:    WordCountMap,
		Reduce: WordCountReduce,
	},
	"invertedindex": {
		Name:   "invertedindex",
		Map:    InvertedIndexMap,
		Reduce: InvertedIndexReduce,
	},
}

// Lookup returns the workload registered under name.
func Lookup(name string) (Workload, error) {
	w, ok := registry[name]
	if !ok {
		return Workload{}, fmt.Errorf("unknown workload %q, have %v", name, Names())
	}
	return w, nil
}

// Names lists the registered workloads in a stable order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
