#!/bin/bash
#
# Build a larger corpus from the committed Project Gutenberg books.
#
# The text is replicated, so per record work is identical to the source corpus
# and only the input volume grows.
#
#   copies mode  writes COPIES separate files per book, each a whole document.
#   concat mode  writes one file per book holding COPIES copies back to back,
#                giving a few large files instead of many small ones. Use it
#                with the split size to control map task granularity.
#
# usage: data/scale.sh COPIES OUTDIR [copies|concat]

set -euo pipefail

copies=${1:?usage: data/scale.sh COPIES OUTDIR [copies|concat]}
outdir=${2:?usage: data/scale.sh COPIES OUTDIR [copies|concat]}
mode=${3:-copies}
srcdir=$(cd "$(dirname "$0")" && pwd)

rm -rf "$outdir"
mkdir -p "$outdir"

for src in "$srcdir"/pg-*.txt; do
    name=$(basename "$src" .txt)
    case "$mode" in
    copies)
        for ((i = 0; i < copies; i++)); do
            cp "$src" "$outdir/$(printf '%s-c%03d.txt' "$name" "$i")"
        done
        ;;
    concat)
        for ((i = 0; i < copies; i++)); do
            cat "$src"
        done > "$outdir/$name.txt"
        ;;
    *)
        echo "unknown mode: $mode" >&2
        exit 1
        ;;
    esac
done

files=$(find "$outdir" -name '*.txt' | wc -l)
bytes=$(du -sb "$outdir" | cut -f1)
echo "wrote $files files, $bytes bytes to $outdir"
