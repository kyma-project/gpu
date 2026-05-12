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

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/kyma-project/gpu/internal/detection"
)

const (
	gpuInstanceType    = "g4dn.xlarge"
	nonGPUInstanceType = "m5.large"
	gardenLinuxOS      = "Garden Linux 1312.3"
	ubuntuOS           = "Ubuntu 22.04"
)

func gpuNode(osImage string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{detection.InstanceTypeLabel: gpuInstanceType},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{OSImage: osImage},
		},
	}
}

func nonGPUNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{detection.InstanceTypeLabel: nonGPUInstanceType},
		},
	}
}

func TestGpuNodeChangedPredicate_Create(t *testing.T) {
	p := gpuNodeChangedPredicate()

	if !p.Create(event.CreateEvent{Object: gpuNode(gardenLinuxOS)}) {
		t.Error("should fire for GPU node creation")
	}
	if p.Create(event.CreateEvent{Object: nonGPUNode()}) {
		t.Error("should not fire for non-GPU node creation")
	}
}

func TestGpuNodeChangedPredicate_Delete(t *testing.T) {
	p := gpuNodeChangedPredicate()

	if !p.Delete(event.DeleteEvent{Object: gpuNode(gardenLinuxOS)}) {
		t.Error("should fire for GPU node deletion")
	}
	if p.Delete(event.DeleteEvent{Object: nonGPUNode()}) {
		t.Error("should not fire for non-GPU node deletion")
	}
}

func TestGpuNodeChangedPredicate_Update(t *testing.T) {
	p := gpuNodeChangedPredicate()

	tests := []struct {
		name string
		old  *corev1.Node
		new  *corev1.Node
		want bool
	}{
		{
			name: "kubelet heartbeat on GPU node - no meaningful change",
			old:  gpuNode(gardenLinuxOS),
			new:  gpuNode(gardenLinuxOS),
			want: false,
		},
		{
			name: "non-GPU node update - irrelevant",
			old:  nonGPUNode(),
			new:  nonGPUNode(),
			want: false,
		},
		{
			name: "GPU node OS image changes (reprovisioned)",
			old:  gpuNode(gardenLinuxOS),
			new:  gpuNode("Garden Linux 1444.0"),
			want: true,
		},
		{
			name: "non-GPU node gains GPU label (node relabeled)",
			old:  nonGPUNode(),
			new:  gpuNode(gardenLinuxOS),
			want: true,
		},
		{
			name: "GPU node loses GPU label (node relabeled)",
			old:  gpuNode(gardenLinuxOS),
			new:  nonGPUNode(),
			want: true,
		},
		{
			name: "GPU node OS changes from Garden Linux to Ubuntu",
			old:  gpuNode(gardenLinuxOS),
			new:  gpuNode(ubuntuOS),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new})
			if got != tt.want {
				t.Errorf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGpuNodeChangedPredicate_Generic(t *testing.T) {
	p := gpuNodeChangedPredicate()
	if p.Generic(event.GenericEvent{Object: gpuNode(gardenLinuxOS)}) {
		t.Error("should never fire for generic events")
	}
}
