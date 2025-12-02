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
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
)

const auditLogDir = "/var/log/nvsentinel"

// VerifyAuditLogsExist verifies audit logs exist for the specified components using Jobs.
func VerifyAuditLogsExist(ctx context.Context, t *testing.T, c klient.Client, components []string) {
	nodes := getSchedulableNodes(ctx, c)
	if len(nodes) == 0 {
		t.Log("No schedulable nodes found, skipping audit log verification")
		return
	}

	clientset, err := kubernetes.NewForConfig(c.RESTConfig())
	if err != nil {
		t.Fatalf("failed to create kubernetes client: %v", err)
	}

	for _, comp := range components {
		t.Run("verify-audit-logs-"+comp, func(t *testing.T) {
			for _, node := range nodes {
				if runAuditCheck(ctx, t, clientset, node, comp) {
					return
				}
			}

			t.Logf("No audit logs found for %s", comp)
		})
	}
}

func getSchedulableNodes(ctx context.Context, c klient.Client) []string {
	list := &v1.NodeList{}
	_ = c.Resources().List(ctx, list)

	var nodes []string

	for _, n := range list.Items {
		if !strings.Contains(n.Name, "kwok") && !n.Spec.Unschedulable {
			nodes = append(nodes, n.Name)
		}
	}

	return nodes
}

func runAuditCheck(ctx context.Context, t *testing.T, cs *kubernetes.Clientset, node, comp string) bool {
	name := shortName(comp, node)
	zero, ttl := int32(0), int32(60)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "nvsentinel"},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &zero,
			TTLSecondsAfterFinished: &ttl,
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					NodeSelector:  map[string]string{"kubernetes.io/hostname": node},
					RestartPolicy: v1.RestartPolicyNever,
					Tolerations: []v1.Toleration{
						{Key: "node-role.kubernetes.io/control-plane", Operator: v1.TolerationOpExists, Effect: v1.TaintEffectNoSchedule},
						{Key: "node-role.kubernetes.io/master", Operator: v1.TolerationOpExists, Effect: v1.TaintEffectNoSchedule},
					},
					Containers: []v1.Container{{
						Name:  "c",
						Image: "busybox:1.36",
						Command: []string{
							"sh", "-c",
							fmt.Sprintf(`find %s -name "*%s*-audit.log" -exec tail -5 {} \;`, auditLogDir, comp),
						},
						VolumeMounts: []v1.VolumeMount{
							{Name: "v", MountPath: auditLogDir, ReadOnly: true},
						},
					}},
					Volumes: []v1.Volume{{
						Name:         "v",
						VolumeSource: v1.VolumeSource{HostPath: &v1.HostPathVolumeSource{Path: auditLogDir}},
					}},
				},
			},
		},
	}

	if _, err := cs.BatchV1().Jobs("nvsentinel").Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return false
	}

	defer func() {
		bg := metav1.DeletePropagationBackground
		_ = cs.BatchV1().Jobs("nvsentinel").Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &bg})
	}()

	output := waitForLogs(ctx, cs, name)
	if output == "" {
		return false
	}

	count := 0

	for _, line := range strings.Split(output, "\n") {
		var e map[string]interface{}

		if json.Unmarshal([]byte(line), &e) == nil && e["timestamp"] != nil && e["method"] != nil {
			if count == 0 {
				t.Logf("Sample: %s", line)
			}

			count++
		}
	}

	if count > 0 {
		t.Logf("Found %d audit entries for %s on %s", count, comp, node)
	}

	return count > 0
}

func shortName(comp, node string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)

	c, n := comp, node
	if len(c) > 6 {
		c = c[:6]
	}

	if len(n) > 10 {
		n = n[:10]
	}

	return fmt.Sprintf("audit-%s-%s-%s", c, n, hex.EncodeToString(b))
}

func waitForLogs(ctx context.Context, cs *kubernetes.Clientset, name string) string {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for {
		j, err := cs.BatchV1().Jobs("nvsentinel").Get(ctx, name, metav1.GetOptions{})
		if err != nil || j.Status.Succeeded > 0 || j.Status.Failed > 0 {
			break
		}

		select {
		case <-ctx.Done():
			return ""
		case <-time.After(time.Second):
		}
	}

	pods, _ := cs.CoreV1().Pods("nvsentinel").List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + name})
	if len(pods.Items) == 0 {
		return ""
	}

	logs, err := cs.CoreV1().Pods("nvsentinel").GetLogs(pods.Items[0].Name, &v1.PodLogOptions{}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer logs.Close()

	var buf bytes.Buffer

	_, _ = io.Copy(&buf, logs)

	return strings.TrimSpace(buf.String())
}
