/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package detection

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	sigs "sigs.k8s.io/controller-runtime/pkg/client"
)

// Outcome represents the result of a pre-flight check.
type Outcome int

const (
	// OutcomeProceed means all checks passed - safe to install or upgrade.
	OutcomeProceed Outcome = iota
	// OutcomeWarn means the cluster is not ready but the condition may be temporary.
	// The reconciler should update status to Warning and requeue.
	OutcomeWarn
	// OutcomeError means a hard blocker was found. The reconciler should update
	// status to Error and stop until the user resolves the issue.
	OutcomeError
)

// PreflightResult is returned by RunPreflight and carries the outcome and a
// human-readable reason the reconciler can surface in the Gpu CR status.
type PreflightResult struct {
	Outcome Outcome
	Reason  string
}

// RunPreflight inspects cluster nodes and determines whether it is safe to proceed
// with installing or upgrading the NVIDIA GPU Operator. It runs at the top of every
// reconcile loop before any Helm operation.
//
// Checks performed (in order):
//  1. Are there any GPU nodes? If not then Warn (nodes may not have joined yet).
//  2. Are all GPU nodes running Garden Linux? If not then Error (v1 supports Garden Linux only).
func RunPreflight(ctx context.Context, c sigs.Client) (PreflightResult, error) {
	var nodeList corev1.NodeList
	if err := c.List(ctx, &nodeList); err != nil {
		return PreflightResult{}, fmt.Errorf("listing nodes: %w", err)
	}

	gpuNodes := filterGPUNodes(nodeList.Items)

	if len(gpuNodes) == 0 {
		return PreflightResult{
			Outcome: OutcomeWarn,
			Reason:  "no GPU nodes found in cluster; waiting for GPU node pool to become available",
		}, nil
	}

	if nonGL := nonGardenLinuxNodes(gpuNodes); len(nonGL) > 0 {
		return PreflightResult{
			Outcome: OutcomeError,
			Reason:  fmt.Sprintf("GPU nodes %v are not running Garden Linux; only Garden Linux is supported in v1", nonGL),
		}, nil
	}

	return PreflightResult{Outcome: OutcomeProceed}, nil
}

// filterGPUNodes returns only the nodes whose instance type label matches a known GPU type.
func filterGPUNodes(nodes []corev1.Node) []corev1.Node {
	var gpu []corev1.Node
	for i := range nodes {
		if IsGPUNode(nodes[i].Labels) {
			gpu = append(gpu, nodes[i])
		}
	}
	return gpu
}

// nonGardenLinuxNodes returns the names of GPU nodes that are not running Garden Linux.
func nonGardenLinuxNodes(nodes []corev1.Node) []string {
	var names []string
	for i := range nodes {
		if !strings.Contains(nodes[i].Status.NodeInfo.OSImage, "Garden Linux") {
			names = append(names, nodes[i].Name)
		}
	}
	return names
}
