#!/usr/bin/env bash

set -euo pipefail

VERSION="${1:-latest}"
OUTPUT_DIR="${2:-internal/chart/values}"
REPO="gardenlinux/gardenlinux-nvidia-installer"

if [ "${VERSION}" = "latest" ]; then
    echo "Resolving latest gardenlinux-nvidia-installer release..."
    VERSION=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | awk -F'"' '{print $4}')
    if [ -z "${VERSION}" ]; then
        echo "ERROR: Could not determine latest release tag from ${REPO}"
        exit 1
    fi
    echo "Resolved latest version: ${VERSION}"
fi

URL="https://raw.githubusercontent.com/${REPO}/refs/tags/${VERSION}/helm/gpu-operator-values.yaml"
OUTPUT_FILE="${OUTPUT_DIR}/gardenlinux.yaml"

echo "Downloading Garden Linux values from ${URL}..."
mkdir -p "${OUTPUT_DIR}"
curl -sSfL -o "${OUTPUT_FILE}" "${URL}"

echo "Successfully downloaded: ${OUTPUT_FILE} ($(wc -c < "${OUTPUT_FILE}" | tr -d ' ') bytes)"
