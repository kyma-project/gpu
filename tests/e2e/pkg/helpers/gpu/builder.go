/*
Copyright 2026 SAP SE or an SAP affiliate company and gpu contributors.

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

// Package gpu provides a fluent builder for the cluster-scoped Gpu CR and
// helpers that apply, update, and tear it down inside an e2e test.
package gpu

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
)

// DefaultName is the singleton name enforced by both CEL and the controller.
const DefaultName = "gpu"

// Builder builds a Gpu CR with a fluent API. Mirrors the pattern from
// kyma-project/istio's IstioCRBuilder.
type Builder struct {
	gpu *gpuv1beta1.Gpu
}

// NewBuilder returns a Builder pre-populated with the singleton name and the
// correct TypeMeta. Tests that want to verify CEL rejection should override
// the name with WithName.
func NewBuilder() *Builder {
	return &Builder{
		gpu: &gpuv1beta1.Gpu{
			TypeMeta: metav1.TypeMeta{
				APIVersion: gpuv1beta1.GroupVersion.String(),
				Kind:       "Gpu",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: DefaultName,
			},
			Spec: gpuv1beta1.GpuSpec{},
		},
	}
}

// WithName overrides the CR name. Use it only to test singleton rejection -
// the controller and the CEL admission rule require the name to be "gpu".
func (b *Builder) WithName(name string) *Builder {
	b.gpu.ObjectMeta.Name = name
	return b
}

// WithDriverVersion pins a specific NVIDIA driver version. When empty, the
// chart default is used.
func (b *Builder) WithDriverVersion(version string) *Builder {
	b.gpu.Spec.Driver = &gpuv1beta1.DriverSpec{Version: version}
	return b
}

// Build returns the assembled Gpu CR.
func (b *Builder) Build() *gpuv1beta1.Gpu {
	return b.gpu
}

// logGpuCR pretty-prints the CR for diagnostic visibility before it lands on
// the apiserver.
func logGpuCR(t *testing.T, gpu *gpuv1beta1.Gpu) {
	t.Helper()
	data, err := json.MarshalIndent(gpu, "", "  ")
	if err != nil {
		t.Logf("Failed to marshal Gpu CR: %v", err)
		return
	}
	t.Logf("Gpu CR to be applied:\n%s", string(data))
}
