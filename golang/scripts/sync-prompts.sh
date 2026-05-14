#!/usr/bin/env bash
# Copy prompts and CONTEXT.csv from the Python implementation into golang/prompts/.
#
# The Go binary //go:embed-s these files. CI runs `make verify-prompts`
# (verify-prompts.sh) to fail loudly on drift.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SRC_DIR="${GO_ROOT}/../server/thor/tools"
DST_DIR="${GO_ROOT}/internal/prompts/embedded"

if [[ ! -d "${SRC_DIR}" ]]; then
    echo "error: source prompts dir not found: ${SRC_DIR}" >&2
    exit 1
fi

mkdir -p "${DST_DIR}"

cp "${SRC_DIR}"/system_old.txt     "${DST_DIR}/system_old.txt"
cp "${SRC_DIR}"/system_new.txt     "${DST_DIR}/system_new.txt"
cp "${SRC_DIR}"/system_revised.txt "${DST_DIR}/system_revised.txt"
cp "${SRC_DIR}"/CONTEXT.csv        "${DST_DIR}/CONTEXT.csv"

echo "synced 4 file(s) from ${SRC_DIR} -> ${DST_DIR}"
