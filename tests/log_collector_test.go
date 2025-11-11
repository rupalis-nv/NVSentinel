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

package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"tests/helpers"
)

// TestLogCollectorFailure tests that remediation continues even when log collector fails
func TestLogCollectorFailure(t *testing.T) {
	feature := features.New("Log Collector - Failure Scenario").
		WithLabel("suite", "log-collector").
		WithLabel("component", "mock-mode").
		WithLabel("scenario", "failure")

	var testCtx *helpers.RemediationTestContext

	feature.Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		var newCtx context.Context
		newCtx, testCtx = helpers.SetupFaultRemediationTest(ctx, t, c, "")

		client, err := c.NewClient()
		require.NoError(t, err)

		// Set mock exit code to 1 to simulate log collector failure scenario
		// Note: KWOK nodes simulate job completion regardless of exit code,
		// but we can verify the configuration is correctly applied
		helpers.SetNodeMockExitCode(newCtx, t, client, testCtx.NodeName, 1)

		return newCtx
	})

	feature.Assess("Trigger fault and verify log collector job is configured for failure", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		client, err := c.NewClient()
		require.NoError(t, err)

		// Snapshot existing jobs BEFORE triggering remediation
		// This ensures we can detect the new job even if it's created very quickly
		t.Log("Snapshotting existing log collector jobs before triggering remediation")
		existingJobs := helpers.SnapshotExistingLogCollectorJobs(ctx, t, client)

		t.Logf("Triggering full remediation flow for node %s", testCtx.NodeName)
		helpers.TriggerFullRemediationFlow(ctx, t, client, testCtx.NodeName, 2)

		t.Logf("Waiting for NEW log collector job to be created for node %s", testCtx.NodeName)
		logCollectorJob := helpers.WaitForNewLogCollectorJob(ctx, t, client, existingJobs)
		t.Logf("Log collector job created: %s", logCollectorJob.GetName())

		// Verify the job has MOCK_EXIT_CODE=1 in its environment
		// Note: KWOK nodes simulate job completion regardless of exit code,
		// so we verify the configuration is correct rather than actual failure
		t.Logf("Verifying log collector job has MOCK_EXIT_CODE=1 configured")
		require.Equal(t, 1, len(logCollectorJob.Spec.Template.Spec.Containers), "job should have exactly one container")
		container := logCollectorJob.Spec.Template.Spec.Containers[0]

		var mockExitCodeValue string
		for _, env := range container.Env {
			if env.Name == "MOCK_EXIT_CODE" {
				mockExitCodeValue = env.Value
				break
			}
		}
		require.Equal(t, "1", mockExitCodeValue, "MOCK_EXIT_CODE should be set to 1")
		t.Logf("✓ Log collector job configured with MOCK_EXIT_CODE=1")

		// Wait for job completion (KWOK nodes will simulate success)
		helpers.WaitForLogCollectorJobCompletion(ctx, t, client, logCollectorJob.GetName())
		t.Log("✓ Log collector job completed (simulated by KWOK node)")

		return ctx
	})

	feature.Assess("Verify RebootNode CR is created (remediation continues)", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		client, err := c.NewClient()
		require.NoError(t, err)

		t.Logf("Verifying RebootNode CR is created, demonstrating remediation continues")
		rebootNodeCR := helpers.WaitForRebootNodeCR(ctx, t, client, testCtx.NodeName)
		require.NotNil(t, rebootNodeCR, "RebootNode CR should be created")

		t.Logf("✓ RebootNode CR created successfully: %s", rebootNodeCR.GetName())
		t.Log("✓ Remediation flow continues (log collector configured for failure scenario)")

		return ctx
	})

	feature.Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		client, _ := c.NewClient()
		if client != nil && testCtx != nil {
			helpers.CleanupNodeMockAnnotations(ctx, t, client, testCtx.NodeName)
		}
		return helpers.TeardownFaultRemediation(ctx, t, c)
	})

	testEnv.Test(t, feature.Feature())
}
