#!/usr/bin/env bash
#
# Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

# This script wraps ko build for Tilt integration
# Usage: ko-tilt-build.sh <module-dir> <expected-ref>

if [ $# -ne 2 ]; then
  echo "Error: Missing required arguments" >&2
  echo "Usage: $0 <module-dir> <expected-ref>" >&2
  exit 1
fi

MODULE_DIR="$1"
EXPECTED_REF="$2"

cd "$MODULE_DIR"

ARCH=$(uname -m)
case "$ARCH" in
  x86_64)
    PLATFORM="linux/amd64"
    ;;
  aarch64|arm64)
    PLATFORM="linux/arm64"
    ;;
  *)
    echo "Error: Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

REPO="${EXPECTED_REF%:*}"
TAG="${EXPECTED_REF##*:}"

KO_DOCKER_REPO="$REPO" ko build --bare --platform=${PLATFORM} --tags="$TAG" ./

echo "$EXPECTED_REF"
