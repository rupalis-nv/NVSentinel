#!/bin/bash
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

# Mock nvidia-bug-report.sh script for testing
# This script simulates the behavior of nvidia-bug-report.sh without requiring actual NVIDIA drivers

set -e

OUTPUT_FILE="nvidia-bug-report.log.gz"

# Parse command-line arguments to find output file
while [[ $# -gt 0 ]]; do
    case $1 in
        --output-file)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --output-file=*)
            OUTPUT_FILE="${1#*=}"
            shift
            ;;
        *)
            shift
            ;;
    esac
done

echo "[MOCK] Generating nvidia-bug-report at ${OUTPUT_FILE}"

# Simulate collection time
if [ -n "${MOCK_SLEEP_DURATION:-}" ] && [ "${MOCK_SLEEP_DURATION}" -gt 0 ]; then
    sleep "${MOCK_SLEEP_DURATION}"
fi

# Create mock content with realistic structure
cat > /tmp/mock-nvidia-bug-report.log <<EOF
Mock NVIDIA Bug Report
============================
Generated: $(date)
Node: ${NODE_NAME:-unknown}
Timestamp: ${TIMESTAMP:-$(date +%s)}

System Information:
-------------------
Hostname: ${NODE_NAME:-mock-node}
Kernel: $(uname -r)
OS: Mock Linux Distribution

NVIDIA Driver Information:
--------------------------
Driver Version: 550.54.15 (Mock)
CUDA Version: 12.4 (Mock)

GPU Information:
----------------
GPU 0: NVIDIA A100-SXM4-80GB (Mock)
  UUID: GPU-12345678-1234-1234-1234-123456789012
  Bus ID: 0000:00:04.0

Mock nvidia-bug-report generated successfully
EOF

# Compress the mock report
gzip -c /tmp/mock-nvidia-bug-report.log > "${OUTPUT_FILE}"
rm -f /tmp/mock-nvidia-bug-report.log

echo "[MOCK] nvidia-bug-report completed successfully"
exit 0

