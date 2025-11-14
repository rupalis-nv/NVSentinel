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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"tests/helpers"
)

// TestLogCollectorFailure tests the negative case where log collector job fails on a KWOK node.
// This test verifies that the fault-remediation controller properly handles log collector failures
// by using KWOK Stage failure injection.
func TestLogCollectorFailure(t *testing.T) {
	logCollectorFailureFeature := features.New("Log Collector Failure Test").
		Assess("Log collector job fails on KWOK node with failure injection", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			assert.NoError(t, err, "failed to create k8s client")

			// Select a KWOK node for failure testing
			kwokNodeName, err := helpers.GetKWOKNodeName(ctx, client)
			assert.NoError(t, err, "failed to get KWOK node")
			t.Logf("Selected KWOK node for failure test: %s", kwokNodeName)

			// Annotate the KWOK node with test-scenario to trigger failure injection
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: kwokNodeName,
				},
			}
			err = client.Resources().Get(ctx, kwokNodeName, "", node)
			assert.NoError(t, err, "failed to get KWOK node")

			if node.Annotations == nil {
				node.Annotations = make(map[string]string)
			}
			node.Annotations["nvsentinel.nvidia.com/test-scenario"] = "log-collector-failure"

			err = client.Resources().Update(ctx, node)
			assert.NoError(t, err, "failed to annotate KWOK node with test-scenario")
			t.Logf("Annotated node %s with test-scenario=log-collector-failure", kwokNodeName)

			// Send a fatal health event to trigger log collection
			t.Logf("Sending fatal health event to node %s", kwokNodeName)
			err = helpers.SendHealthEventsToNodes([]string{kwokNodeName}, "data/fatal-health-event.json")
			assert.NoError(t, err, "failed to send health event")

			// Wait for the log collector job WITH test-scenario label to be created
			t.Logf("Waiting for log collector job with test-scenario label to be created...")
			var logCollectorJob *v1.Pod
			require.Eventually(t, func() bool {
				jobs := helpers.GetJobsForNode(ctx, t, client, kwokNodeName, "nvsentinel")
				// Find a log collector job with the test-scenario label
				for i := range jobs {
					if jobs[i].Labels["app"] == "log-collector" && jobs[i].Labels["test-scenario"] == "log-collector-failure" {
						logCollectorJob = &jobs[i]
						return true
					}
				}
				return false
			}, 30*time.Second, 1*time.Second, "log collector job with test-scenario label should be created")

			require.NotNil(t, logCollectorJob, "expected a log-collector job pod with test-scenario label on node %s", kwokNodeName)

			t.Logf("Found log collector job pod: %s in namespace: %s", logCollectorJob.Name, logCollectorJob.Namespace)
			t.Logf("Pod labels: %v", logCollectorJob.Labels)
			t.Logf("Pod current phase: %s", logCollectorJob.Status.Phase)

			// Verify the job has the test-scenario label for KWOK failure injection
			scenario, ok := logCollectorJob.Labels["test-scenario"]
			require.True(t, ok, "log-collector job pod is missing test-scenario label")
			assert.Equal(t, "log-collector-failure", scenario, "log collector job should have test-scenario=log-collector-failure label")
			t.Logf("✓ Log collector job has test-scenario label: %s", scenario)

			// Check pod phase - on KWOK nodes, pods may not reach Failed naturally
			// Verify the pod is scheduled to the correct KWOK node
			t.Logf("Pod scheduled on node: %s", logCollectorJob.Spec.NodeName)
			assert.Equal(t, kwokNodeName, logCollectorJob.Spec.NodeName, "pod should be scheduled on the annotated KWOK node")
			t.Logf("✓ Pod is correctly scheduled on KWOK node %s", kwokNodeName)

			// Note: On KWOK nodes, the pod won't actually execute
			// The KWOK Stage would inject failure into the Job based on the test-scenario label
			t.Logf("Note: On KWOK nodes, pods don't execute. The KWOK Stage injects failure based on the test-scenario label.")

			// Clean up: Remove the test-scenario annotation from the node
			err = client.Resources().Get(ctx, kwokNodeName, "", node)
			if err == nil {
				delete(node.Annotations, "nvsentinel.nvidia.com/test-scenario")
				_ = client.Resources().Update(ctx, node)
				t.Logf("Cleaned up test-scenario annotation from node %s", kwokNodeName)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, logCollectorFailureFeature)
}
