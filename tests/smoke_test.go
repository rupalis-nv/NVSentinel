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

	"github.com/nvidia/nvsentinel/commons/pkg/statemanager"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"tests/helpers"
)

type testContextKey int

const (
	keyNodeName testContextKey = iota
	keyNamespace
)

func TestFatalHealthEvent(t *testing.T) {
	feature := features.New("TestFatalHealthEventEndToEnd").
		WithLabel("suite", "smoke")

	feature.Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		workloadNamespace := "workloads"

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		nodes, err := helpers.GetAllNodesNames(ctx, client)
		assert.NoError(t, err, "failed to get cluster nodes")
		assert.True(t, len(nodes) > 0, "no nodes found in cluster")

		// Select from nodes that scale test never touched (starting from 50% onwards)
		// Scale test uses first 45% of nodes, so selecting from 50%+ ensures no MongoDB collision
		startIdx := int(float64(len(nodes)) * 0.50)
		if startIdx >= len(nodes) {
			startIdx = len(nodes) - 1
		}
		unusedNodes := nodes[startIdx:]

		var nodeName string
		for _, name := range unusedNodes {
			node, err := helpers.GetNodeByName(ctx, client, name)
			if err != nil {
				t.Logf("failed to get node %s: %v", name, err)
				continue
			}
			if !node.Spec.Unschedulable {
				nodeName = name
				break
			}
		}

		if nodeName == "" {
			nodeName = unusedNodes[0]
			t.Logf("No uncordoned node in unused subset, using first unused node: %s", nodeName)
		} else {
			t.Logf("Selected uncordoned node: %s (from unused nodes starting at index %d)", nodeName, startIdx)
		}

		err = helpers.CreateNamespace(ctx, client, workloadNamespace)
		assert.NoError(t, err, "failed to create workloads namespace")

		podTemplate := helpers.NewGPUPodSpec(workloadNamespace, 1)
		helpers.CreatePodsAndWaitTillRunning(ctx, t, client, []string{nodeName}, podTemplate)

		ctx = context.WithValue(ctx, keyNodeName, nodeName)
		ctx = context.WithValue(ctx, keyNamespace, workloadNamespace)

		return ctx
	})

	nodeLabelSequenceObserved := make(chan bool)
	feature.Assess("Can start node label watcher", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		nodeName := ctx.Value(keyNodeName).(string)
		t.Logf("Starting label sequence watcher for node %s", nodeName)
		desiredNVSentinelStateNodeLabels := []string{
			string(statemanager.QuarantinedLabelValue),
			string(statemanager.DrainingLabelValue),
			string(statemanager.DrainSucceededLabelValue),
			string(statemanager.RemediatingLabelValue),
			string(statemanager.RemediationSucceededLabelValue),
		}
		err = helpers.StartNodeLabelWatcher(ctx, t, client, nodeName, desiredNVSentinelStateNodeLabels, nodeLabelSequenceObserved)
		assert.NoError(t, err, "failed to start node label watcher")
		return ctx
	})

	feature.Assess("Can send fatal health event", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		err := helpers.SendHealthEventsToNodes([]string{nodeName}, "data/fatal-health-event.json")
		assert.NoError(t, err, "failed to send health event")

		return ctx
	})

	feature.Assess("Node is cordoned", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		t.Logf("Waiting for node %s to be cordoned", nodeName)
		helpers.WaitForNodesCordonState(ctx, t, client, []string{nodeName}, true)

		node, err := helpers.GetNodeByName(ctx, client, nodeName)
		assert.NoError(t, err, "failed to get node after cordoning")

		assert.Equal(t, "NVSentinel", node.Labels["k8saas.nvidia.com/cordon-by"])
		assert.Equal(t, "GPU-fatal-error-ruleset", node.Labels["k8saas.nvidia.com/cordon-reason"])

		var nodeCondition *v1.NodeCondition
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == "GpuXidError" {
				nodeCondition = &node.Status.Conditions[i]
				break
			}
		}
		assert.NotNil(t, nodeCondition, "node condition GpuXidError not found")

		assert.Equal(t, "GpuXidError", string(nodeCondition.Type))
		assert.Equal(t, "True", string(nodeCondition.Status))
		assert.Equal(t, "GpuXidErrorIsNotHealthy", nodeCondition.Reason)
		assert.Equal(t, "ErrorCode:79 gpu:0 XID error occurred Recommended Action=RESTART_VM;", nodeCondition.Message)

		return ctx
	})

	feature.Assess("Drain label is set and pods are not evicted, delete the pod to move the process forward", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)
		namespaceName := ctx.Value(keyNamespace).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		helpers.WaitForNodesWithLabel(ctx, t, client, []string{nodeName}, statemanager.NVSentinelStateLabelKey, string(statemanager.DrainingLabelValue))

		expectedDrainingNodeEvent := v1.Event{
			Type:   "NodeDraining",
			Reason: "AwaitingPodCompletion",
		}
		helpers.WaitForNodeEvent(ctx, t, client, nodeName, expectedDrainingNodeEvent)

		helpers.DrainRunningPodsInNamespace(ctx, t, client, namespaceName)

		return ctx
	})

	feature.Assess("Remediation CR is created and completes", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		rebootNode := helpers.WaitForRebootNodeCR(ctx, t, client, nodeName)

		err = helpers.DeleteRebootNodeCR(ctx, client, rebootNode)
		assert.NoError(t, err, "failed to delete RebootNode CR")

		return ctx
	})

	feature.Assess("Can send healthy event", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		err := helpers.SendHealthEventsToNodes([]string{nodeName}, "data/healthy-event.json")
		assert.NoError(t, err, "failed to send health event")

		return ctx
	})

	feature.Assess("Node is uncordoned", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		t.Logf("Waiting for node %s to be uncordoned", nodeName)
		helpers.WaitForNodesCordonState(ctx, t, client, []string{nodeName}, false)

		node, err := helpers.GetNodeByName(ctx, client, nodeName)
		assert.NoError(t, err, "failed to get node after uncordoning")

		assert.Equal(t, "NVSentinel", node.Labels["k8saas.nvidia.com/uncordon-by"])

		var nodeCondition *v1.NodeCondition
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == "GpuXidError" {
				nodeCondition = &node.Status.Conditions[i]
				break
			}
		}
		assert.NotNil(t, nodeCondition, "node condition GpuXidError not found")

		assert.Equal(t, "GpuXidError", string(nodeCondition.Type))
		assert.Equal(t, "False", string(nodeCondition.Status))
		assert.Equal(t, "GpuXidErrorIsHealthy", nodeCondition.Reason)
		assert.Equal(t, "No Health Failures", nodeCondition.Message)

		return ctx
	})

	feature.Assess("Observed NVSentinel expected state label changes", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		select {
		case success := <-nodeLabelSequenceObserved:
			assert.True(t, success)
		default:
			assert.Fail(t, "did not observe expected label changes for nvsentinel-state")
		}
		return ctx
	})

	feature.Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		namespaceName := ctx.Value(keyNamespace).(string)
		err = helpers.DeleteNamespace(ctx, t, client, namespaceName)
		assert.NoError(t, err, "failed to delete workloads namespace")

		return ctx
	})

	testEnv.Test(t, feature.Feature())
}

func TestFatalUnsupportedHealthEvent(t *testing.T) {
	feature := features.New("TestFatalUnsupportedHealthEventEndToEnd").
		WithLabel("suite", "smoke")

	feature.Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		workloadNamespace := "workloads"

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		nodes, err := helpers.GetAllNodesNames(ctx, client)
		assert.NoError(t, err, "failed to get cluster nodes")
		assert.True(t, len(nodes) > 0, "no nodes found in cluster")

		startIdx := int(float64(len(nodes)) * 0.50)
		if startIdx >= len(nodes) {
			startIdx = len(nodes) - 1
		}
		unusedNodes := nodes[startIdx:]

		var nodeName string
		for _, name := range unusedNodes {
			node, err := helpers.GetNodeByName(ctx, client, name)
			if err != nil {
				t.Logf("failed to get node %s: %v", name, err)
				continue
			}
			if !node.Spec.Unschedulable {
				nodeName = name
				break
			}
		}

		if nodeName == "" {
			nodeName = unusedNodes[0]
			t.Logf("No uncordoned node in unused subset, using first unused node: %s", nodeName)
		} else {
			t.Logf("Selected uncordoned node: %s (from unused nodes starting at index %d)", nodeName, startIdx)
		}

		err = helpers.CreateNamespace(ctx, client, workloadNamespace)
		assert.NoError(t, err, "failed to create workloads namespace")

		podTemplate := helpers.NewGPUPodSpec(workloadNamespace, 1)
		helpers.CreatePodsAndWaitTillRunning(ctx, t, client, []string{nodeName}, podTemplate)

		ctx = context.WithValue(ctx, keyNodeName, nodeName)
		ctx = context.WithValue(ctx, keyNamespace, workloadNamespace)

		return ctx
	})

	feature.Assess("Can send fatal health event", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		err := helpers.SendHealthEventsToNodes([]string{nodeName}, "data/unsupported-health-event.json")
		assert.NoError(t, err, "failed to send health event")

		return ctx
	})

	feature.Assess("Node is cordoned", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		t.Logf("Waiting for node %s to be cordoned", nodeName)
		helpers.WaitForNodesCordonState(ctx, t, client, []string{nodeName}, true)

		node, err := helpers.GetNodeByName(ctx, client, nodeName)
		assert.NoError(t, err, "failed to get node after cordoning")

		assert.Equal(t, "NVSentinel", node.Labels["k8saas.nvidia.com/cordon-by"])
		assert.Equal(t, "GPU-fatal-error-ruleset", node.Labels["k8saas.nvidia.com/cordon-reason"])

		var nodeCondition *v1.NodeCondition
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == "GpuXidError" {
				nodeCondition = &node.Status.Conditions[i]
				break
			}
		}
		assert.NotNil(t, nodeCondition, "node condition GpuXidError not found")

		assert.Equal(t, "GpuXidError", string(nodeCondition.Type))
		assert.Equal(t, "True", string(nodeCondition.Status))
		assert.Equal(t, "GpuXidErrorIsNotHealthy", nodeCondition.Reason)
		assert.Equal(t, "ErrorCode:145 gpu:0 XID error occurred Recommended Action=CONTACT_SUPPORT;", nodeCondition.Message)

		return ctx
	})

	feature.Assess("Drain label is set and pods are not evicted, delete the pod to move the process forward", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)
		namespaceName := ctx.Value(keyNamespace).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		helpers.WaitForNodesWithLabel(ctx, t, client, []string{nodeName}, statemanager.NVSentinelStateLabelKey, string(statemanager.DrainingLabelValue))

		expectedDrainingNodeEvent := v1.Event{
			Type:   "NodeDraining",
			Reason: "AwaitingPodCompletion",
		}
		helpers.WaitForNodeEvent(ctx, t, client, nodeName, expectedDrainingNodeEvent)

		helpers.DrainRunningPodsInNamespace(ctx, t, client, namespaceName)

		return ctx
	})

	feature.Assess("Remediation failed label is set", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		helpers.WaitForNodesWithLabel(ctx, t, client, []string{nodeName}, statemanager.NVSentinelStateLabelKey, string(statemanager.RemediationFailedLabelValue))

		return ctx
	})

	feature.Assess("Can send healthy event", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		err := helpers.SendHealthEventsToNodes([]string{nodeName}, "data/healthy-event.json")
		assert.NoError(t, err, "failed to send health event")

		return ctx
	})

	feature.Assess("Node is uncordoned", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		nodeName := ctx.Value(keyNodeName).(string)

		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		t.Logf("Waiting for node %s to be uncordoned", nodeName)
		helpers.WaitForNodesCordonState(ctx, t, client, []string{nodeName}, false)

		node, err := helpers.GetNodeByName(ctx, client, nodeName)
		assert.NoError(t, err, "failed to get node after uncordoning")

		assert.Equal(t, "NVSentinel", node.Labels["k8saas.nvidia.com/uncordon-by"])

		var nodeCondition *v1.NodeCondition
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == "GpuXidError" {
				nodeCondition = &node.Status.Conditions[i]
				break
			}
		}
		assert.NotNil(t, nodeCondition, "node condition GpuXidError not found")

		assert.Equal(t, "GpuXidError", string(nodeCondition.Type))
		assert.Equal(t, "False", string(nodeCondition.Status))
		assert.Equal(t, "GpuXidErrorIsHealthy", nodeCondition.Reason)
		assert.Equal(t, "No Health Failures", nodeCondition.Message)

		return ctx
	})

	feature.Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		client, err := c.NewClient()
		assert.NoError(t, err, "failed to create kubernetes client")

		namespaceName := ctx.Value(keyNamespace).(string)
		err = helpers.DeleteNamespace(ctx, t, client, namespaceName)
		assert.NoError(t, err, "failed to delete workloads namespace")

		return ctx
	})

	testEnv.Test(t, feature.Feature())
}
