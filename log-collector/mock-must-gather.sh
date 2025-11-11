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

# Mock must-gather.sh script for testing
# This script simulates the behavior of GPU Operator must-gather without requiring actual GPU Operator

set -e

OUTPUT_DIR="${PWD}"
OUTPUT_FILE="must-gather.tar.gz"

# Parse command-line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -o|--output)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

echo "[MOCK] Generating GPU Operator must-gather"

# Simulate collection time
if [ -n "${MOCK_SLEEP_DURATION:-}" ] && [ "${MOCK_SLEEP_DURATION}" -gt 0 ]; then
    sleep "${MOCK_SLEEP_DURATION}"
fi

# Create mock must-gather content
TEMP_DIR=$(mktemp -d)
mkdir -p "${TEMP_DIR}/gpu-operator-must-gather"

cat > "${TEMP_DIR}/gpu-operator-must-gather/summary.txt" <<EOF
Mock GPU Operator Must-Gather Report
=====================================
Generated: $(date)
Node: ${NODE_NAME:-unknown}
Timestamp: ${TIMESTAMP:-$(date +%s)}
Namespace: ${GPU_OPERATOR_NAMESPACE:-gpu-operator}

GPU Operator Pods:
------------------
- nvidia-driver-daemonset-xxxxx (Mock)
- nvidia-device-plugin-daemonset-xxxxx (Mock)
- gpu-feature-discovery-xxxxx (Mock)

GPU Operator Version: v23.9.0 (Mock)

Mock must-gather completed successfully
EOF

# Create mock pod logs
mkdir -p "${TEMP_DIR}/gpu-operator-must-gather/logs"
echo "Mock driver pod logs - Node: ${NODE_NAME:-unknown}" > "${TEMP_DIR}/gpu-operator-must-gather/logs/nvidia-driver.log"
echo "Mock device plugin logs - Node: ${NODE_NAME:-unknown}" > "${TEMP_DIR}/gpu-operator-must-gather/logs/device-plugin.log"

# Create tarball
cd "${TEMP_DIR}"
tar czf "${OUTPUT_FILE}" gpu-operator-must-gather/

# Move to output directory
mv "${OUTPUT_FILE}" "${OUTPUT_DIR}/"

# Cleanup
cd - > /dev/null
rm -rf "${TEMP_DIR}"

echo "[MOCK] GPU Operator must-gather completed: ${OUTPUT_DIR}/${OUTPUT_FILE}"
exit 0

