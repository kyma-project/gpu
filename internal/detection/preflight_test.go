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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sigs "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func gpuNode(name, instanceType, osImage string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				instanceTypeLabel: instanceType,
			},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{OSImage: osImage},
		},
	}
}

func systemNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{instanceTypeLabel: "m5.xlarge"},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{OSImage: "Ubuntu 22.04"},
		},
	}
}

func buildClient(nodes ...runtime.Object) sigs.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return fakeclient.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(nodes...).Build()
}

func TestRunPreflight(t *testing.T) {
	tests := []struct {
		name        string
		nodes       []runtime.Object
		wantOutcome Outcome
	}{
		{
			name:        "no nodes at all — warn",
			nodes:       nil,
			wantOutcome: OutcomeWarn,
		},
		{
			name:        "only system nodes, no GPU nodes — warn",
			nodes:       []runtime.Object{systemNode("system-1"), systemNode("system-2")},
			wantOutcome: OutcomeWarn,
		},
		{
			name: "GPU nodes all Garden Linux AWS — proceed",
			nodes: []runtime.Object{
				gpuNode("gpu-1", "g4dn.xlarge", "Garden Linux 1592.1"),
				gpuNode("gpu-2", "g6.xlarge", "Garden Linux 1592.1"),
				systemNode("system-1"),
			},
			wantOutcome: OutcomeProceed,
		},
		{
			name: "GPU nodes all Garden Linux GCP — proceed",
			nodes: []runtime.Object{
				gpuNode("gpu-1", "g2-standard-8", "Garden Linux 1592.1"),
			},
			wantOutcome: OutcomeProceed,
		},
		{
			name: "GPU node running Ubuntu — error",
			nodes: []runtime.Object{
				gpuNode("gpu-1", "g4dn.xlarge", "Ubuntu 22.04"),
			},
			wantOutcome: OutcomeError,
		},
		{
			name: "mixed GPU nodes — some Garden Linux, some Ubuntu — error",
			nodes: []runtime.Object{
				gpuNode("gpu-1", "g4dn.xlarge", "Garden Linux 1592.1"),
				gpuNode("gpu-2", "g6.xlarge", "Ubuntu 22.04"),
			},
			wantOutcome: OutcomeError,
		},
		{
			name: "system nodes Ubuntu, GPU nodes Garden Linux — proceed",
			nodes: []runtime.Object{
				systemNode("system-1"),
				gpuNode("gpu-1", "g4dn.xlarge", "Garden Linux 1592.1"),
			},
			wantOutcome: OutcomeProceed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := buildClient(tt.nodes...)
			result, err := RunPreflight(context.Background(), c)
			if err != nil {
				t.Fatalf("RunPreflight() unexpected error: %v", err)
			}
			if result.Outcome != tt.wantOutcome {
				t.Errorf("RunPreflight() outcome = %v, want %v; reason: %q",
					result.Outcome, tt.wantOutcome, result.Reason)
			}
		})
	}
}
