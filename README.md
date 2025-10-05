# MapReduce

Please make sure to regularly commit and push your work to Github. As with all assignments in this course, 10% of the grade will come from the quality of your git commit history.

## Introduction

In this lab you'll build a MapReduce system. You'll implement a worker process that calls application Map and Reduce functions and handles reading and writing files, and a coordinator process that hands out tasks to workers and copes with failed workers. You'll be building something similar to the [MapReduce paper](http://static.googleusercontent.com/media/research.google.com/en//archive/mapreduce-osdi04.pdf).

## Getting started

We supply you with a simple sequential mapreduce implementation in `mr-main/mrsequential.go`. It runs the maps and reduces one at a time, in a single process. We also provide you with a couple of MapReduce applications: word-count in `mrapps/wc.go`, and a text indexer in `mrapps/indexer.go`. MapReduce applications are built into shared object files (`*.so`) and then loaded into the MapReduce engine.

`mrsequential.go` leaves its output in the file `mr-out-0`. The input is from the text files named `pg-xxx.txt` in the `data` folder.

You can run word count sequentially as follows:

```bash
$ cd mr-main
# build a shared object plugin out of the wordcount application
$ go build -buildmode=plugin ../mrapps/wc.go
# clean up any leftover result files
$ rm mr-out*
# run word count
$ go run mrsequential.go wc.so ../data/pg*.txt
$ head mr-out-0 
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

# build indexer
$ go build -buildmode=plugin ../mrapps/indexer.go
# run indexer
$ go run mrsequential.go indexer.so ../data/pg*.txt
$ head mr-out-0 
A 8 ../data/pg-being_ernest.txt,../data/pg-dorian_gray.txt,../data/pg-frankenstein.txt,../data/pg-grimm.txt,../data/pg-huckleberry_finn.txt,../data/pg-metamorphosis.txt,../data/pg-sherlock_holmes.txt,../data/pg-tom_sawyer.txt
ABOUT 1 ../data/pg-tom_sawyer.txt
ACT 1 ../data/pg-being_ernest.txt
ACTRESS 1 ../data/pg-dorian_gray.txt
ACTUAL 8 ../data/pg-being_ernest.txt,../data/pg-dorian_gray.txt,../data/pg-frankenstein.txt,../data/pg-grimm.txt,../data/pg-huckleberry_finn.txt,../data/pg-metamorphosis.txt,../data/pg-sherlock_holmes.txt,../data/pg-tom_sawyer.txt
ADLER 1 ../data/pg-sherlock_holmes.txt
ADVENTURE 1 ../data/pg-sherlock_holmes.txt
ADVENTURES 4 ../data/pg-grimm.txt,../data/pg-huckleberry_finn.txt,../data/pg-sherlock_holmes.txt,../data/pg-tom_sawyer.txt
AFTER 2 ../data/pg-huckleberry_finn.txt,../data/pg-tom_sawyer.txt
AGREE 8 ../data/pg-being_ernest.txt,../data/pg-dorian_gray.txt,../data/pg-frankenstein.txt,../data/pg-grimm.txt,../data/pg-huckleberry_finn.txt,../data/pg-metamorphosis.txt,../data/pg-sherlock_holmes.txt,../data/pg-tom_sawyer.txt
# look up a specific result
$ grep Twain mr-out-0
Twain 2 ../data/pg-huckleberry_finn.txt,../data/pg-tom_sawyer.txt
```

So the word `Twain` only appeared in two files: Huck Finn and Tom Sawyer, as we would expect.

Feel free to borrow code from `mrsequential.go`. You should also have a look at `mrapps/wc.go` to see what MapReduce application code looks like.

## Your Job

Your job is to implement a distributed MapReduce, consisting of two programs, the **coordinator** and the **worker**. There will be just one coordinator process, and one or more worker processes executing in parallel. In a real system the workers would run on a bunch of different machines, but for this lab you'll run them all on a single machine. The workers will talk to the coordinator via RPC. Each worker process will ask the coordinator for a task, read the task's input from one or more files, execute the task, and write the task's output to one or more files. The coordinator should notice if a worker hasn't completed its task in a reasonable amount of time (for this lab, use **ten seconds**), and give the same task to a different worker.

We have given you a little code to start you off. The "main" routines for the coordinator and worker are in `mr-main/mrcoordinator.go` and `mr-main/mrworker.go`; **don't change these files**. You should put your implementation in `mr/coordinator.go`, `mr/worker.go`, and `mr/rpc.go`.

You will also need to implement a `tf-idf` MapReduce application (see below).

Here's how to run your code on the word-count MapReduce application. First, make sure the word-count plugin is freshly built:

```bash
$ go build -buildmode=plugin ../mrapps/wc.go
```

In the `mr-main` directory, run the coordinator.

```bash
$ rm mr-out*
$ go run mrcoordinator.go ../data/pg-*.txt
```

The `../data/pg-*.txt` arguments to `mrcoordinator.go` are the input files; each file corresponds to one "split", and is the input to one Map task.

In one or more _separate terminal windows_, run some workers:

```bash
$ go run mrworker.go wc.so
```

When the workers and coordinator have finished, look at the output in `mr-out-*`. When you've completed the lab, the sorted union of the output files should match the sequential output, like this:

```bash
$ cat mr-out-* | sort | more
A 509
ABOUT 2
ACT 8
...
```

We supply you with a test script in `mr-main/test-mr.sh`. The tests check that the `wc` and `indexer` MapReduce applications produce the correct output when given the `pg-xxx.txt` files as input. The tests also check that your implementation runs the Map and Reduce tasks in parallel, and that your implementation recovers from workers that crash while running tasks.

If you run the test script now, it will hang because the coordinator never finishes:

```bash
$ cd mr-main
$ bash test-mr.sh
*** Starting wc test.
```

You can change `ret := false` to true in the `Done` function in `mr/coordinator.go` so that the coordinator exits immediately. Then:

```bash
$ bash ./test-mr.sh
*** Starting wc test.
sort: No such file or directory
cmp: EOF on mr-wc-all
--- wc output is not the same as mr-correct-wc.txt
--- wc test: FAIL
```

The test script expects to see output in files named `mr-out-X`, one for each reduce task. The empty implementations of `mr/coordinator.go` and `mr/worker.go` don't produce those files (or do much of anything else), so the test fails.

When you've finished, the test script output should look like this:

```bash
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

You'll also see some errors from the Go RPC package that look like

```
2019/12/16 13:27:09 rpc.Register: method "Done" has 1 input parameters; needs exactly three
```

Ignore these messages.

### `tf-idf`

Once you've implemented MapReduce, you'll need to implement a new application on top of it.

**tf-idf** (_term frequency - inverse document frequency_) measures the importance of a word in a set of documents. If `tf-idf(word, document)` is high, this suggests `word` is "relevant" within `document` (i.e., maybe this document would be a good search result), whereas if `tf-idf` is low, then the word and the document are not related. Words can have a low `tf-idf` if they are (a) uncommon in a document, or (b) common in all documents. We compute `tf-idf` as:

$$
\textrm{tf-idf}(\mathsf{term}, \mathsf{doc})=\mathrm{tf}(\mathsf{term}, \mathsf{doc})\cdot\mathrm{idf}(\mathsf{term})
$$

where
- $\mathrm{tf}(t, d)=\textrm{term-count}(t, d)/\textrm{doc-length}(d)$, and
- $\mathrm{idf}(t)=\log(N/\textrm{docs-containing}(t))$
- $N$ is the total number of documents

Here's some pseudocode:

```python
def tf-idf(term, doc):
  tc = doc.count(term)
  dl = doc.wordCount() # total number of words
  tf = tc / dl

  dc = sum(1 if (term in d) else 0 for d in ALL_DOCS)

  # assume NUM_DOCS is a system constant
  idf = log(NUM_DOCS / dc)

  return tf * idf
```

The last part of your assignment is to implement a MapReduce application that computes `tf-idf`. Completing the _full_ quantity requires some cleverness with how you implement your `Map()` and `Reduce()` functions.

You should compute a slightly modified `tf-idf`:

1. `tf-idf` can be quite small, so to make it more legible, multiply the final result by `1e7` (ten million), and then round to an integer.
2. If a term appears in no documents, `dc == 0` and we could get a divide by zero. On the other hand, if a term occurs in all documents, we take `log(1) = 0` and `tf-idf == 0`, which we also don't want. Therefore, we take `log(1 + N / (1 + dc))`.

Specifically, your MapReduce job should compute something like this:

```python
def modified-tf-idf(term, doc):
  # ...

  return round(1e7 * (tc / dl) * log(1 + NUM_DOCS / (1 + dc)))
```

Your final output should contain one entry per word, with a list of documents that contain it, and their scaled `tf-idf`. For example:

```
...
monster [{pg-grimm.txt 126} {pg-dorian_gray.txt 119} {pg-being_ernest.txt 137} {pg-frankenstein.txt 1310} {pg-tom_sawyer.txt 43} {pg-metamorphosis.txt 130}]
...
```

Assuming slice `out` contains your `document, tf-idf` pairs, returning

```go
fmt.Sprintf("%v", out)
```

for your `Reduce` output will achieve this format. Order of files within each list does not matter.

- You can assume `NUM_DOCS = 8` (the number of files in the `data/` directory) as a constant. (Computing this value within the MapReduce job does not seem to be possible.)
- A Python example is provided in the `mrapps/` directory. ([link](./mrapps/tf-idf-example.py)) The output of this file will be the basis for correctness test of your submission. Run it like this: `python3 tf-idf-example.py ../data/pg-*`
- To get `tf-idf` working, you _will_ need to modify both `Map` and `Reduce`. You will need pieces from both `wc` and `indexer`. You also may need to use a different representation in the `KeyValue` pair (but stick with strings).
- Think carefully about how it makes sense to divide work between Mappers and Reducers.
- Filenames should be "trimmed" to their basename (not full paths). The `Base()` function from package `path` will do the job.
- Transform all input files into lowercase: search results should not be case sensitive.
- Use the base-10 logarithm.

## Assignment Rules

* The map phase should divide the intermediate keys into buckets for `nReduce` reduce tasks, where `nReduce` is the argument that `mr-main/mrcoordinator.go` passes to `MakeCoordinator()`.
* The worker implementation should put the output of the X'th reduce task in the file `mr-out-X`.
* A `mr-out-X` file should contain one line per Reduce function output. The line should be generated with the Go `"%v %v"` format, called with the key and value. Have a look in `mr-main/mrsequential.go` for the line commented "this is the correct format". The test script will fail if your implementation deviates too much from this format.
* You can modify `mr/worker.go`, `mr/coordinator.go`, and `mr/rpc.go`. You can temporarily modify other files for testing, but make sure your code works with the original versions; we'll test with the original versions.
* The worker should put intermediate Map output in files in the current directory, where your worker can later read them as input to Reduce tasks. 
* `mr-main/mrcoordinator.go` expects `mr/coordinator.go` to implement a `Done()` method that returns true when the MapReduce job is completely finished; at that point, `mrcoordinator.go` will exit.
* When the job is completely finished, the worker processes should exit. A simple way to implement this is to use the return value from `call()`: if the worker fails to contact the coordinator, it can assume that the coordinator has exited because the job is done, and so the worker can terminate too. Depending on your design, you might also find it helpful to have a "please exit" pseudo-task that the coordinator can give to workers.
* You will need to submit all files you change: `worker.go`, `coordinator.go`, `rpc.go`, and `tfidf.go`. Do not submit any other files.

## Hints

* One way to get started is to modify `mr/worker.go`'s `Worker()` to send an RPC to the coordinator asking for a task. Then modify the coordinator to respond with the file name of an as-yet-unstarted map task. Then modify the worker to read that file and call the application Map function, as in `mrsequential.go`.
* The application Map and Reduce functions are loaded at run-time using the Go plugin package, from files whose names end in `.so`.
* If you change anything in the `mr/` directory, you will probably have to re-build any MapReduce plugins you use, with something like `go build -buildmode=plugin ../mrapps/wc.go`.
* This lab relies on the workers sharing a file system. That's straightforward when all workers run on the same machine, but would require a global filesystem like GFS if the workers ran on different machines.
* A reasonable naming convention for intermediate files is `mr-X-Y`, where `X` is the Map task number, and `Y` is the reduce task number.
* The worker's map task code will need a way to store intermediate key/value pairs in files in a way that can be correctly read back during reduce tasks. One possibility is to use Go's `encoding/json` package. To write key/value pairs to a JSON file:
```go
  enc := json.NewEncoder(file)
  for _, kv := ... {
    err := enc.Encode(&kv)
  }
```
and to read such a file back:
```go
  dec := json.NewDecoder(file)
  for {
    var kv KeyValue
    if err := dec.Decode(&kv); err != nil {
      break
    }
    kva = append(kva, kv)
  }
```
* The map part of your worker can use the `ihash(key)` function (in `worker.go`) to pick the reduce task for a given key.
* You can steal some code from `mrsequential.go` for reading Map input files, for sorting intermedate key/value pairs between the Map and Reduce, and for storing Reduce output in files.
* The coordinator, as an RPC server, will be concurrent; don't forget to lock shared data.
* Use Go's race detector, with `go build -race` and `go run -race`. `test-mr.sh` has a comment that shows you how to enable the race detector for the tests.
* Workers will sometimes need to wait, e.g. reduces can't start until the last map has finished. One possibility is for workers to periodically ask the coordinator for work, sleeping with `time.Sleep()` between each request. Another possibility is for the relevant RPC handler in the coordinator to have a loop that waits, either with `time.Sleep()` or `sync.Cond`. Go runs the handler for each RPC in its own thread, so the fact that one handler is waiting won't prevent the coordinator from processing other RPCs.
* The coordinator can't reliably distinguish between crashed workers, workers that are alive but have stalled for some reason, and workers that are executing but too slowly to be useful. The best you can do is have the coordinator wait for some amount of time, and then give up and re-issue the task to a different worker. For this lab, have the coordinator wait for ten seconds; after that the coordinator should assume the worker has died (of course, it might not have).
* To test crash recovery, you can use the `mrapps/crash.go` application plugin. It randomly exits in the Map and Reduce functions.
* To ensure that nobody observes partially written files in the presence of crashes, the MapReduce paper mentions the trick of using a temporary file and atomically renaming it once it is completely written. You can use `ioutil.TempFile` to create a temporary file and `os.Rename` to atomically rename it.
* `test-mr.sh` runs all the processes in the sub-directory `mr-tmp`, so if something goes wrong and you want to look at intermediate or output files, look there.
* If you are a Windows user and use WSL, you might have to do `dos2unix test-mr.sh` before running the test script (do this in case you get weird errors when run `bash test-mr.sh`).

**Good luck!**
