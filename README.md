# Distributed MapReduce in Go

This project is a Go implementation of a distributed MapReduce system, inspired by the original [Google MapReduce paper](http://static.googleusercontent.com/media/research.google.com/en//archive/mapreduce-osdi04.pdf).

It consists of a central **coordinator** that manages tasks and one or more **worker** processes that execute Map and Reduce tasks in parallel. The system is designed to be fault-tolerant, capable of handling worker failures by re-assigning their tasks.

## Core Architecture

* **Coordinator (`mr/coordinator.go`):**

  * Acts as the central authority for task management.

  * Hands out Map and Reduce tasks to available workers.

  * Monitors worker progress and implements a 10-second timeout. If a worker fails to complete a task in time, the task is re-assigned to another worker.

  * Tracks the overall state of the MapReduce job (Map phase, Reduce phase, Complete).

* **Worker (`mr/worker.go`):**

  * Requests tasks from the coordinator via RPC.

  * Loads the application-specific Map and Reduce functions from a Go plugin file (e.g., `wc.so`).

  * Reads input files for Map tasks, partitions intermediate key/value pairs, and writes them to local intermediate files.

  * Reads intermediate files for Reduce tasks, sorts the data, and runs the Reduce function.

  * Atomically writes final output to `mr-out-X` files.

* **RPC (`mr/rpc.go`):**

  * Defines the RPC structures and methods used for communication between the coordinator and workers.

## Included Applications

This framework can run any MapReduce application built as a Go plugin. This repository includes three examples:

1. **Word Count (`mrapps/wc.go`):** The classic MapReduce example. It counts the occurrences of each word in a set of text files.

2. **Indexer (`mrapps/indexer.go`):** Creates an inverted index, mapping each word to a list of documents in which it appears.

3. **TF-IDF (`mrapps/tfidf.go`):**
   A more complex application that calculates the *Term Frequency-Inverse Document Frequency* for words in documents. This implementation computes a modified `tf-idf` score:

   $$
   \\ \textrm{tf-idf}(\mathsf{term}, \mathsf{doc})=\mathrm{tf}(\mathsf{term}, \mathsf{doc})\cdot\mathrm{idf}(\mathsf{term})
   $$

   \$$$$Where the final result is scaled and rounded using the formula:
   `round(1e7 * (tc / dl) * log(1 + NUM_DOCS / (1 + dc)))`

## How to Run

You can run the full distributed system locally.

### 1. Build an Application Plugin

First, build the desired MapReduce application (e.g., word count) into a shared object (`.so`) file.

```
# From the mr-main directory
$cd mr-main$ go build -buildmode=plugin ../mrapps/wc.go

```

### 2. Start the Coordinator

The coordinator takes the input files as arguments. Each file will be treated as a single Map task.

```
# Clean up previous output files
$ rm mr-out*

# Run the coordinator
$ go run mrcoordinator.go ../data/pg-*.txt

```

### 3. Start Workers

In one or more *separate terminal windows*, run worker processes. The worker will automatically find and connect to the coordinator.

```
# Run a worker
$ go run mrworker.go wc.so

```

You can run multiple worker processes to execute tasks in parallel.

### 4. View the Output

Once the coordinator indicates the job is complete, it will exit. The final output will be in the `mr-out-*` files. You can check the result by sorting the output, which should match the sequential implementation.

```
$ cat mr-out-* | sort | head
A 509
ABOUT 2
ACT 8
ACTRESS 1
ACTUAL 8
ADLER 1
ADVENTURE 12
ADVENTURES 7
AFTER 2
AGREE 16

```

## Testing

The project includes a comprehensive test script that verifies correctness, parallelism, and fault tolerance (including worker crashes).

To run the tests:

```
$cd mr-main$ bash ./test-mr.sh

```

A successful run will produce the following output:

```
$ bash ./test-mr.sh
*** Starting wc test.
--- wc test: PASS
*** Starting indexer test.
--- indexer test: PASS
*** Starting map parallelism test.
--- map parallelism test: PASS
*** Starting reduce parallelism test.
--- reduce parallelism test: PASS
*** Starting crash test.
--- crash test: PASS
*** PASSED ALL TESTS

```
