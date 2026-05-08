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

import "testing"

func TestIsGPUNode(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		want         bool
	}{
		// AWS - only G4dn and G6 are offered via Kyma
		{"aws g4dn", "g4dn.xlarge", true},
		{"aws g6", "g6.xlarge", true},
		{"aws g5 - not offered via Kyma", "g5.xlarge", false},
		{"aws g4ad - AMD GPU, not NVIDIA", "g4ad.xlarge", false},
		{"aws p4d - not offered via Kyma", "p4d.24xlarge", false},
		// GCP - only G2 series offered via Kyma
		{"gcp g2", "g2-standard-8", true},
		{"gcp g2 large", "g2-standard-48", true},
		{"gcp a2 - not offered via Kyma", "a2-highgpu-8g", false},
		{"gcp a3 - not offered via Kyma", "a3-megagpu-8g", false},
		// Azure - only NC series offered via Kyma
		{"azure NC", "Standard_NC4as_T4_v3", true},
		{"azure NC64", "Standard_NC64as_T4_v3", true},
		{"azure ND - not offered via Kyma", "Standard_ND96asr_v4", false},
		{"azure NV - not offered via Kyma", "Standard_NV12s_v3", false},
		// non-GPU
		{"aws m5", "m5.xlarge", false},
		{"gcp n2", "n2-standard-4", false},
		{"azure D4s", "Standard_D4s_v3", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGPUNode(map[string]string{instanceTypeLabel: tt.instanceType})
			if got != tt.want {
				t.Errorf("IsGPUNode(%q) = %v, want %v", tt.instanceType, got, tt.want)
			}
		})
	}
}

func TestIsGPUNodeMissingLabel(t *testing.T) {
	if IsGPUNode(map[string]string{}) {
		t.Error("IsGPUNode with no labels should return false")
	}
}

func TestIsGPUNodeEmptyLabel(t *testing.T) {
	if IsGPUNode(map[string]string{instanceTypeLabel: ""}) {
		t.Error("IsGPUNode with empty instance-type label should return false")
	}
}
