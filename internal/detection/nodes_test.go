package detection

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func node(name, osImage string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{OSImage: osImage},
		},
	}
}

func TestAllNodesGardenLinux(t *testing.T) {
	tests := []struct {
		name  string
		nodes []runtime.Object
		want  bool
	}{
		{
			name:  "no nodes",
			nodes: nil,
			want:  false,
		},
		{
			name:  "all ubuntu",
			nodes: []runtime.Object{node("n1", "Ubuntu 22.04"), node("n2", "Ubuntu 22.04")},
			want:  false,
		},
		{
			name:  "all garden linux",
			nodes: []runtime.Object{node("n1", "Garden Linux 1592.1"), node("n2", "Garden Linux 1592.1")},
			want:  true,
		},
		{
			name:  "mixed",
			nodes: []runtime.Object{node("n1", "Ubuntu 22.04"), node("n2", "Garden Linux 1592.1")},
			want:  false,
		},
		{
			name:  "single garden linux node",
			nodes: []runtime.Object{node("n1", "Garden Linux 1592.1")},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			c := fakeclient.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.nodes...).Build()

			got, err := AllNodesGardenLinux(context.Background(), c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("AllNodesGardenLinux() = %v, want %v", got, tt.want)
			}
		})
	}
}
