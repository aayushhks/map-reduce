#!/usr/bin/env python3
#
# TF-IDF example.
# Turn me into a MapReduce job!

import sys
from collections import Counter, defaultdict
import re
from pathlib import Path
import math

wordlist = Counter()
doclen = Counter()
inverted_index = defaultdict(set)

files = sys.argv[1:]

for f in files:
    # In golang, you can use strings.FieldsFunc
    with open(f) as txt:
        # lowercase!
        words = re.split(r'[^a-zA-Z]+', txt.read().lower())

    # compute basename
    docname = Path(f).name
    for word in words:
        if word:
            # Count of this word in doc
            wordlist[(word, docname)] += 1

            # Count of all words in doc
            doclen[docname] += 1

            inverted_index[word].add(docname)

NUM_DOCS = 8

output = defaultdict(list)

for ((term, doc), freq) in wordlist.items():
    tf = float(freq) / doclen[doc]
    idf = math.log10(1 + NUM_DOCS / (1.0 + len(inverted_index[term])))
    scaled = round(1e7 * tf * idf)
    output[term].append(f"{{{doc} {scaled}}}")

for (k, v) in sorted(output.items()):
    print(k, f"[{' '.join(v)}]")