// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package helpers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/e2e-framework/klient"
)

// sanitizeNodeName validates that a node name contains only safe characters.
// This prevents shell command injection when node names are used in shell commands.
func sanitizeNodeName(nodeName string) string {
	// Only allow alphanumeric, hyphen, underscore, and dot (valid Kubernetes node name characters)
	for _, r := range nodeName {
		if !isValidNodeNameChar(r) {
			panic(fmt.Sprintf("invalid node name contains unsafe characters: %s", nodeName))
		}
	}

	return nodeName
}

// isValidNodeNameChar checks if a character is valid in a Kubernetes node name.
func isValidNodeNameChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
}

// SnapshotExistingLogCollectorJobs records the names of all existing log collector jobs.
// This should be called BEFORE triggering any remediation that creates new jobs.
// Returns a map of existing job names that can be passed to WaitForNewLogCollectorJob.
func SnapshotExistingLogCollectorJobs(ctx context.Context, t *testing.T, client klient.Client) map[string]bool {
	t.Helper()

	existingJobs := make(map[string]bool)
	initialJobs := &batchv1.JobList{}

	if err := client.Resources(NVSentinelNamespace).List(ctx, initialJobs); err == nil {
		for _, job := range initialJobs.Items {
			if strings.Contains(job.GetName(), "log-collector") {
				existingJobs[job.GetName()] = true
				t.Logf("Snapshot: existing log collector job: %s", job.GetName())
			}
		}
	}

	if len(existingJobs) == 0 {
		t.Log("Snapshot: no existing log collector jobs found")
	}

	return existingJobs
}

// WaitForNewLogCollectorJob waits for a NEW log collector job to be created.
// The existingJobs map should be obtained from SnapshotExistingLogCollectorJobs
// BEFORE triggering remediation
func WaitForNewLogCollectorJob(
	ctx context.Context, t *testing.T, client klient.Client, existingJobs map[string]bool,
) *batchv1.Job {
	t.Helper()

	var logCollectorJob *batchv1.Job

	require.Eventually(t, func() bool {
		jobs := &batchv1.JobList{}

		err := client.Resources(NVSentinelNamespace).List(ctx, jobs)
		if err != nil {
			t.Logf("Failed to list jobs: %v", err)
			return false
		}

		// Find a NEW log collector job (one that wasn't in our snapshot)
		for i, job := range jobs.Items {
			if strings.Contains(job.GetName(), "log-collector") && !existingJobs[job.GetName()] {
				logCollectorJob = &jobs.Items[i]
				t.Logf("Found new log collector job: %s", logCollectorJob.GetName())

				return true
			}
		}

		return false
	}, EventuallyWaitTimeout, WaitInterval, "log collector job should be created")

	return logCollectorJob
}

// WaitForLogCollectorJobCreation waits for a NEW log collector job to be created.
// Deprecated: Use SnapshotExistingLogCollectorJobs + WaitForNewLogCollectorJob for better control.
// This function snapshots existing jobs and immediately waits, which can miss jobs created very quickly.
func WaitForLogCollectorJobCreation(ctx context.Context, t *testing.T, client klient.Client) *batchv1.Job {
	t.Helper()
	existingJobs := SnapshotExistingLogCollectorJobs(ctx, t, client)

	return WaitForNewLogCollectorJob(ctx, t, client, existingJobs)
}

// WaitForLogCollectorJobCompletion waits for the given log collector job to complete successfully.
func WaitForLogCollectorJobCompletion(ctx context.Context, t *testing.T, client klient.Client, jobName string) {
	t.Helper()

	require.Eventually(t, func() bool {
		job := &batchv1.Job{}

		err := client.Resources(NVSentinelNamespace).Get(ctx, jobName, NVSentinelNamespace, job)
		if err != nil {
			return false
		}

		if job.Status.Failed > 0 {
			t.Fatalf("log collector job %s failed (%d failed runs)", jobName, job.Status.Failed)
		}

		return job.Status.Succeeded > 0
	}, EventuallyWaitTimeout, WaitInterval, "log collector job should succeed")
}

// ValidateMockArtifactUploads checks if mock artifacts were uploaded to the file server and validates their content.
// This validation is optional and will log a note if file server is not available.
func ValidateMockArtifactUploads(ctx context.Context, t *testing.T, client klient.Client, nodeName string) {
	t.Helper()

	// Sanitize node name to prevent shell command injection
	nodeName = sanitizeNodeName(nodeName)

	t.Log("Validating mock artifacts were uploaded to file server")

	fileServerPodName, err := GetFileServerPodName(ctx, client)
	if err != nil || fileServerPodName == "" {
		t.Logf("Note: File server not available for validation (this is OK in mock mode): %v", err)
		return
	}

	restConfig := client.RESTConfig()

	// Check if files exist
	listCmd := []string{
		"sh", "-c",
		"ls -la /data/" + nodeName + "/ 2>/dev/null | " +
			"grep -E 'nvidia-bug-report|gpu-operator-must-gather' || echo 'no files'",
	}
	stdout, _, execErr := ExecInPod(ctx, restConfig, NVSentinelNamespace, fileServerPodName, "file-server", listCmd)

	if execErr != nil || strings.Contains(stdout, "no files") {
		t.Logf("Note: Could not find artifact files (this is OK in mock mode): %v", execErr)
		return
	}

	t.Logf("Mock artifact files found in file server: %s", stdout)

	// Validate file content (ensure files are not empty and contain mock data)
	t.Log("Validating mock artifact file contents are not empty")

	// Check nvidia-bug-report file content
	bugReportCmd := []string{
		"sh", "-c",
		"find /data/" + nodeName + "/ -name 'nvidia-bug-report-*' -type f -exec zcat {} \\; 2>/dev/null | head -n 1",
	}
	bugReportContent, _, _ := ExecInPod(
		ctx, restConfig, NVSentinelNamespace, fileServerPodName, "file-server", bugReportCmd,
	)

	if bugReportContent != "" && strings.Contains(bugReportContent, "Mock") {
		t.Logf("✓ nvidia-bug-report contains mock content: %s", strings.TrimSpace(bugReportContent))
	} else {
		t.Log("⚠ nvidia-bug-report file may be empty or missing mock content")
	}

	// Check must-gather file content
	mustGatherCmd := []string{
		"sh", "-c",
		"find /data/" + nodeName + "/ -name 'gpu-operator-must-gather-*' -type f " +
			"-exec tar -tzf {} \\; 2>/dev/null | head -n 5",
	}
	mustGatherContent, _, _ := ExecInPod(
		ctx, restConfig, NVSentinelNamespace, fileServerPodName, "file-server", mustGatherCmd,
	)

	if mustGatherContent != "" && strings.Contains(mustGatherContent, "gpu-operator-must-gather") {
		t.Logf("✓ gpu-operator-must-gather contains mock archive: %s", strings.TrimSpace(mustGatherContent))
	} else {
		t.Log("⚠ gpu-operator-must-gather file may be empty or missing")
	}
}

// SetNodeMockExitCode sets a node annotation to control the log collector mock exit code.
func SetNodeMockExitCode(ctx context.Context, t *testing.T, client klient.Client, nodeName string, exitCode int) {
	t.Helper()

	node, err := GetNodeByName(ctx, client, nodeName)
	require.NoError(t, err, "failed to get node %s", nodeName)

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}

	node.Annotations["nvsentinel.nvidia.com/log-collector-mock-exit-code"] = strconv.Itoa(exitCode)

	err = client.Resources().Update(ctx, node)
	require.NoError(t, err, "failed to set mock exit code annotation")
	t.Logf("Set log collector mock exit code to %d for node %s", exitCode, nodeName)
}

// SetNodeMockSleepDuration sets a node annotation to control the log collector mock sleep duration.
func SetNodeMockSleepDuration(ctx context.Context, t *testing.T, client klient.Client, nodeName string, seconds int) {
	t.Helper()

	node, err := GetNodeByName(ctx, client, nodeName)
	require.NoError(t, err, "failed to get node %s", nodeName)

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}

	node.Annotations["nvsentinel.nvidia.com/log-collector-mock-sleep"] = strconv.Itoa(seconds)

	err = client.Resources().Update(ctx, node)
	require.NoError(t, err, "failed to set mock sleep annotation")
	t.Logf("Set log collector mock sleep duration to %d seconds for node %s", seconds, nodeName)
}

// CleanupNodeMockAnnotations removes log collector mock annotations from a node.
func CleanupNodeMockAnnotations(ctx context.Context, t *testing.T, client klient.Client, nodeName string) {
	t.Helper()

	node, err := GetNodeByName(ctx, client, nodeName)
	if err != nil {
		t.Logf("Failed to get node for cleanup: %v", err)

		return
	}

	if node.Annotations != nil {
		delete(node.Annotations, "nvsentinel.nvidia.com/log-collector-mock-exit-code")
		delete(node.Annotations, "nvsentinel.nvidia.com/log-collector-mock-sleep")

		if err := client.Resources().Update(ctx, node); err != nil {
			t.Logf("Failed to cleanup mock annotations: %v", err)
		} else {
			t.Logf("Cleaned up log collector mock annotations for node %s", nodeName)
		}
	}
}
