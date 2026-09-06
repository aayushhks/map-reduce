#!/bin/bash
#
# Build a larger corpus from the committed Project Gutenberg books.
#
# Each source book is copied COPIES times under a distinct document name. The
# text is replicated, so per record work is identical to the source corpus and
# only the input volume grows. Document names stay distinct, so the inverted
# index treats every copy as its own document.
#
# usage: data/scale.sh COPIES OUTDIR

set -euo pipefail

copies=${1:?usage: data/scale.sh COPIES OUTDIR}
outdir=${2:?usage: data/scale.sh COPIES OUTDIR}
srcdir=$(cd "$(dirname "$0")" && pwd)

rm -rf "$outdir"
mkdir -p "$outdir"

for src in "$srcdir"/pg-*.txt; do
    name=$(basename "$src" .txt)
    for ((i = 0; i < copies; i++)); do
        cp "$src" "$outdir/$(printf '%s-c%03d.txt' "$name" "$i")"
    done
done

files=$(find "$outdir" -name '*.txt' | wc -l)
bytes=$(du -sb "$outdir" | cut -f1)
echo "wrote $files files, $bytes bytes to $outdir"
