#!/usr/bin/env bash

set -euo pipefail

VERSION="${1:-latest}"
REPO_URL="${2:-https://helm.ngc.nvidia.com/nvidia}"
OUTPUT_DIR="${3:-internal/chart/gpu-operator}"

INDEX_URL="${REPO_URL}/index.yaml"

if [ "${VERSION}" = "latest" ]; then
    echo "Fetching Helm repo index from ${INDEX_URL}..."
    TMPFILE=$(mktemp)
    trap 'rm -f "${TMPFILE}"' EXIT
    curl -sSfL -o "${TMPFILE}" "${INDEX_URL}"
    VERSION=$(awk '/^  gpu-operator:/{found=1} found && /^    version:/{print $2; exit}' "${TMPFILE}" | tr -d '"')
    if [ -z "${VERSION}" ]; then
        echo "ERROR: Could not determine latest chart version from ${INDEX_URL}"
        exit 1
    fi
    echo "Resolved latest version: ${VERSION}"
fi

CHART_URL="${REPO_URL}/charts/gpu-operator-${VERSION}.tgz"
OUTPUT_FILE="${OUTPUT_DIR}/gpu-operator-${VERSION}.tgz"

echo "Downloading gpu-operator chart ${VERSION} from ${CHART_URL}..."
curl -sSfL -o "${OUTPUT_FILE}" "${CHART_URL}"

echo "Successfully downloaded: ${OUTPUT_FILE} ($(wc -c < "${OUTPUT_FILE}" | tr -d ' ') bytes)"
