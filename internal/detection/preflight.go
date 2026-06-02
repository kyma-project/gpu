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

// OSType identifies the operating system running on a GPU node.
type OSType string

const (
	OSTypeGardenLinux OSType = "gardenlinux"
	OSTypeUbuntu      OSType = "ubuntu"
	OSTypeUnknown     OSType = ""
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
	OS      OSType
}

// RunPreflight inspects cluster nodes and determines whether it is safe to proceed
// with installing or upgrading the NVIDIA GPU Operator. It runs at the top of every
// reconcile loop before any Helm operation.
//
// Checks performed (in order):
//  1. Are there any GPU nodes? If not then Warn (nodes may not have joined yet).
//  2. Do all GPU nodes run a supported OS (Garden Linux or Ubuntu)? If not then Error.
//  3. Do all GPU nodes run the same OS? Mixed clusters are not supported at this point; Error if mixed.
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

	var unsupported []string
	osTypes := map[OSType]bool{}
	for _, n := range gpuNodes {
		os, ok := detectNodeOS(n)
		if !ok {
			unsupported = append(unsupported, fmt.Sprintf("%s (%s)", n.Name, n.Status.NodeInfo.OSImage))
			continue
		}
		osTypes[os] = true
	}

	if len(unsupported) > 0 {
		// Unsupported nodes are checked before mixed-OS: if any node has an unknown OS
		// it is excluded from osTypes, so the mixed-OS check below would fire on a
		// partial view of the cluster. Reporting unsupported nodes first gives a clearer
		// error and avoids the misleading "mixed OS" message.
		return PreflightResult{
			Outcome: OutcomeError,
			Reason:  fmt.Sprintf("GPU nodes with unsupported OS: %v; supported operating systems: Garden Linux, Ubuntu", unsupported),
		}, nil
	}

	if len(osTypes) > 1 {
		return PreflightResult{
			Outcome: OutcomeError,
			Reason:  "GPU nodes are running mixed operating systems; all GPU nodes must run the same OS (Garden Linux or Ubuntu)",
		}, nil
	}

	// At this point: no unsupported nodes, no mixed OS, so osTypes contains exactly
	// one entry. Range-iterate to extract it (len == 1 is guaranteed by the checks above).
	var detectedOS OSType
	for os := range osTypes {
		detectedOS = os
	}

	return PreflightResult{Outcome: OutcomeProceed, OS: detectedOS}, nil
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

// detectNodeOS identifies the OS type from node.Status.NodeInfo.OSImage.
// Returns false if the OS is not one of the supported types.
func detectNodeOS(node corev1.Node) (OSType, bool) {
	img := strings.ToLower(node.Status.NodeInfo.OSImage)
	switch {
	case strings.Contains(img, "garden linux"):
		return OSTypeGardenLinux, true
	case strings.Contains(img, "ubuntu"):
		return OSTypeUbuntu, true
	default:
		return OSTypeUnknown, false
	}
}
