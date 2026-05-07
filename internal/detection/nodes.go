package detection

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	sigs "sigs.k8s.io/controller-runtime/pkg/client"
)

// AllNodesGardenLinux lists cluster nodes and returns true only if every node
// runs Garden Linux. Returns false when the cluster has no nodes.
func AllNodesGardenLinux(ctx context.Context, c sigs.Client) (bool, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return false, fmt.Errorf("listing nodes: %w", err)
	}

	if len(nodes.Items) == 0 {
		return false, nil
	}

	for i := range nodes.Items {
		if !strings.Contains(nodes.Items[i].Status.NodeInfo.OSImage, "Garden Linux") {
			return false, nil
		}
	}
	return true, nil
}
