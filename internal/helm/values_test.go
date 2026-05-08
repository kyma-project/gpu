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

package helm

import (
	"testing"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
)

func driverSpec(version string) *gpuv1beta1.DriverSpec {
	return &gpuv1beta1.DriverSpec{Version: version}
}

func TestBuildValues(t *testing.T) {
	tests := []struct {
		name    string
		spec    gpuv1beta1.GpuSpec
		cluster ClusterInfo
		// driver map expectations
		wantRepo          string
		wantVersion       string
		wantVersionAbsent bool
		// keys that must be present in the top-level map (from gardenlinux.yaml base)
		wantTopLevelKeys []string
	}{
		{
			name:              "garden linux - no driver version override uses embedded base",
			spec:              gpuv1beta1.GpuSpec{},
			cluster:           ClusterInfo{GardenLinux: true},
			wantRepo:          "ghcr.io/gardenlinux/gardenlinux-nvidia-installer/1.7.1",
			wantVersionAbsent: false,
			wantVersion:       "590", // default from gardenlinux.yaml
			wantTopLevelKeys:  []string{"cdi", "toolkit", "node-feature-discovery"},
		},
		{
			name:             "garden linux - spec version overrides embedded base default",
			spec:             gpuv1beta1.GpuSpec{Driver: driverSpec("595")},
			cluster:          ClusterInfo{GardenLinux: true},
			wantRepo:         "ghcr.io/gardenlinux/gardenlinux-nvidia-installer/1.7.1",
			wantVersion:      "595",
			wantTopLevelKeys: []string{"cdi", "toolkit", "node-feature-discovery"},
		},
		{
			name:             "garden linux - nil driver spec uses embedded base default",
			spec:             gpuv1beta1.GpuSpec{Driver: nil},
			cluster:          ClusterInfo{GardenLinux: true},
			wantRepo:         "ghcr.io/gardenlinux/gardenlinux-nvidia-installer/1.7.1",
			wantVersion:      "590",
			wantTopLevelKeys: []string{"cdi", "toolkit", "node-feature-discovery"},
		},
		{
			name:              "ubuntu - no driver version override",
			spec:              gpuv1beta1.GpuSpec{},
			cluster:           ClusterInfo{GardenLinux: false},
			wantRepo:          nvidiaDriverRepo,
			wantVersionAbsent: true,
		},
		{
			name:        "ubuntu - with driver version override",
			spec:        gpuv1beta1.GpuSpec{Driver: driverSpec("535.129.03")},
			cluster:     ClusterInfo{GardenLinux: false},
			wantRepo:    nvidiaDriverRepo,
			wantVersion: "535.129.03",
		},
		{
			name:              "ubuntu - nil driver spec",
			spec:              gpuv1beta1.GpuSpec{Driver: nil},
			cluster:           ClusterInfo{GardenLinux: false},
			wantRepo:          nvidiaDriverRepo,
			wantVersionAbsent: true,
		},
		{
			name:              "ubuntu - empty version string treated as absent",
			spec:              gpuv1beta1.GpuSpec{Driver: driverSpec("")},
			cluster:           ClusterInfo{GardenLinux: false},
			wantRepo:          nvidiaDriverRepo,
			wantVersionAbsent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildValues(tt.spec, tt.cluster)
			if err != nil {
				t.Fatalf("BuildValues() unexpected error: %v", err)
			}

			// check required top-level keys from the embedded base
			for _, key := range tt.wantTopLevelKeys {
				if _, exists := got[key]; !exists {
					t.Errorf("top-level key %q absent (expected from gardenlinux.yaml base)", key)
				}
			}

			driverRaw, ok := got["driver"]
			if !ok {
				t.Fatal("BuildValues: missing 'driver' key")
			}
			driverMap, ok := driverRaw.(map[string]interface{})
			if !ok {
				t.Fatalf("BuildValues: 'driver' is %T, want map[string]interface{}", driverRaw)
			}

			assertString(t, driverMap, "repository", tt.wantRepo)

			if tt.wantVersionAbsent {
				if _, exists := driverMap["version"]; exists {
					t.Errorf("driver.version = %v, want key absent", driverMap["version"])
				}
			} else {
				assertString(t, driverMap, "version", tt.wantVersion)
			}
		})
	}
}

func assertString(t *testing.T, m map[string]interface{}, key, want string) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("key %q absent, want %q", key, want)
		return
	}
	s, ok := got.(string)
	if !ok {
		t.Errorf("key %q is %T, want string", key, got)
		return
	}
	if s != want {
		t.Errorf("key %q = %q, want %q", key, s, want)
	}
}
