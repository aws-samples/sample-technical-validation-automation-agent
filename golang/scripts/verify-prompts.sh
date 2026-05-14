#!/usr/bin/env bash
# Fail (non-zero exit) if golang/prompts/ has drifted from server/thor/tools/.
# Used in CI to catch prompt edits that didn't round-trip through `make sync-prompts`.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SRC_DIR="${GO_ROOT}/../server/thor/tools"
DST_DIR="${GO_ROOT}/internal/prompts/embedded"

if [[ ! -d "${SRC_DIR}" ]]; then
    echo "error: source prompts dir not found: ${SRC_DIR}" >&2
    exit 1
fi
if [[ ! -d "${DST_DIR}" ]]; then
    echo "error: prompts dir not found: ${DST_DIR}; run 'make sync-prompts'" >&2
    exit 1
fi

drift=0
for name in system_old.txt system_new.txt system_revised.txt CONTEXT.csv; do
    if ! diff -q "${SRC_DIR}/${name}" "${DST_DIR}/${name}" >/dev/null; then
        echo "drift: ${name} differs between server/thor/tools/ and golang/prompts/" >&2
        drift=1
    fi
done

if [[ "${drift}" -eq 1 ]]; then
    echo "" >&2
    echo "Run 'make sync-prompts' to refresh golang/prompts/." >&2
    exit 1
fi

echo "prompts in sync"
