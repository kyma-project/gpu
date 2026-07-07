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
	"fmt"
	"strings"
	"testing"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/internal/detection"
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
			cluster:           ClusterInfo{OS: detection.OSTypeGardenLinux},
			wantRepo:          "ghcr.io/gardenlinux/gardenlinux-nvidia-installer/1.13.2",
			wantVersionAbsent: false,
			wantVersion:       "590", // default from gardenlinux.yaml
			wantTopLevelKeys:  []string{"cdi", "toolkit", "node-feature-discovery"},
		},
		{
			name:             "garden linux - spec version overrides embedded base default",
			spec:             gpuv1beta1.GpuSpec{Driver: driverSpec("595")},
			cluster:          ClusterInfo{OS: detection.OSTypeGardenLinux},
			wantRepo:         "ghcr.io/gardenlinux/gardenlinux-nvidia-installer/1.13.2",
			wantVersion:      "595",
			wantTopLevelKeys: []string{"cdi", "toolkit", "node-feature-discovery"},
		},
		{
			name:             "garden linux - nil driver spec uses embedded base default",
			spec:             gpuv1beta1.GpuSpec{Driver: nil},
			cluster:          ClusterInfo{OS: detection.OSTypeGardenLinux},
			wantRepo:         "ghcr.io/gardenlinux/gardenlinux-nvidia-installer/1.13.2",
			wantVersion:      "590",
			wantTopLevelKeys: []string{"cdi", "toolkit", "node-feature-discovery"},
		},
		{
			name:              "ubuntu - no driver version override uses NVIDIA defaults",
			spec:              gpuv1beta1.GpuSpec{},
			cluster:           ClusterInfo{OS: detection.OSTypeUbuntu},
			wantRepo:          nvidiaDriverRepo,
			wantVersionAbsent: true,
		},
		{
			name:        "ubuntu - with driver version override",
			spec:        gpuv1beta1.GpuSpec{Driver: driverSpec("535.129.03")},
			cluster:     ClusterInfo{OS: detection.OSTypeUbuntu},
			wantRepo:    nvidiaDriverRepo,
			wantVersion: "535.129.03",
		},
		{
			name:              "ubuntu - nil driver spec",
			spec:              gpuv1beta1.GpuSpec{Driver: nil},
			cluster:           ClusterInfo{OS: detection.OSTypeUbuntu},
			wantRepo:          nvidiaDriverRepo,
			wantVersionAbsent: true,
		},
		{
			name:              "ubuntu - empty version string treated as absent",
			spec:              gpuv1beta1.GpuSpec{Driver: driverSpec("")},
			cluster:           ClusterInfo{OS: detection.OSTypeUbuntu},
			wantRepo:          nvidiaDriverRepo,
			wantVersionAbsent: true,
		},
		{
			name:             "time-slicing enabled - devicePlugin.config keys are set",
			spec:             gpuv1beta1.GpuSpec{TimeSlicing: &gpuv1beta1.TimeSlicingSpec{Replicas: 4}},
			cluster:          ClusterInfo{OS: detection.OSTypeGardenLinux},
			wantRepo:         "ghcr.io/gardenlinux/gardenlinux-nvidia-installer/1.13.2",
			wantVersion:      "590",
			wantTopLevelKeys: []string{"cdi", "toolkit", "node-feature-discovery"},
		},
		{
			name:              "time-slicing disabled - devicePlugin.config keys are absent",
			spec:              gpuv1beta1.GpuSpec{},
			cluster:           ClusterInfo{OS: detection.OSTypeUbuntu},
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
			driverMap, ok := driverRaw.(map[string]any)
			if !ok {
				t.Fatalf("BuildValues: 'driver' is %T, want map[string]any", driverRaw)
			}

			assertString(t, driverMap, "repository", tt.wantRepo)

			if tt.wantVersionAbsent {
				if _, exists := driverMap["version"]; exists {
					t.Errorf("driver.version = %v, want key absent", driverMap["version"])
				}
			} else {
				assertString(t, driverMap, "version", tt.wantVersion)
			}

			if tt.spec.TimeSlicing != nil {
				dpRaw, ok := got["devicePlugin"]
				if !ok {
					t.Fatal("devicePlugin key absent when TimeSlicing is set")
				}
				dpMap, ok := dpRaw.(map[string]any)
				if !ok {
					t.Fatalf("devicePlugin is %T, want map[string]any", dpRaw)
				}
				cfgRaw, ok := dpMap["config"]
				if !ok {
					t.Fatal("devicePlugin.config key absent when TimeSlicing is set")
				}
				cfgMap, ok := cfgRaw.(map[string]any)
				if !ok {
					t.Fatalf("devicePlugin.config is %T, want map[string]any", cfgRaw)
				}
				if create, _ := cfgMap["create"].(bool); !create {
					t.Errorf("devicePlugin.config.create = %v, want true", cfgMap["create"])
				}
				assertString(t, cfgMap, "name", "gpu-time-slicing-config-4")
				assertString(t, cfgMap, "default", "any")
				dataRaw, ok := cfgMap["data"]
				if !ok {
					t.Fatal("devicePlugin.config.data key absent when TimeSlicing is set")
				}
				dataMap, ok := dataRaw.(map[string]any)
				if !ok {
					t.Fatalf("devicePlugin.config.data is %T, want map[string]any", dataRaw)
				}
				anyVal, ok := dataMap["any"].(string)
				if !ok {
					t.Fatalf("devicePlugin.config.data[\"any\"] is %T, want string", dataMap["any"])
				}
				wantReplicas := fmt.Sprintf("replicas: %d", tt.spec.TimeSlicing.Replicas)
				if !contains(anyVal, wantReplicas) {
					t.Errorf("devicePlugin.config.data[\"any\"] = %q, want it to contain %q", anyVal, wantReplicas)
				}
			} else {
				if dp, ok := got["devicePlugin"]; ok {
					dpMap, _ := dp.(map[string]any)
					if cfg, hasCfg := dpMap["config"]; hasCfg {
						t.Errorf("devicePlugin.config = %v, want key absent when TimeSlicing is nil", cfg)
					}
				}
			}
		})
	}
}

func assertString(t *testing.T, m map[string]any, key, want string) {
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

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestTimeSlicingConfigMapName(t *testing.T) {
	t.Run("includes replica count in name", func(t *testing.T) {
		spec := gpuv1beta1.GpuSpec{TimeSlicing: &gpuv1beta1.TimeSlicingSpec{Replicas: 4}}
		if got := TimeSlicingConfigMapName(spec); got != "gpu-time-slicing-config-4" {
			t.Errorf("TimeSlicingConfigMapName() = %q, want %q", got, "gpu-time-slicing-config-4")
		}
	})

	t.Run("different replicas produce different names", func(t *testing.T) {
		spec4 := gpuv1beta1.GpuSpec{TimeSlicing: &gpuv1beta1.TimeSlicingSpec{Replicas: 4}}
		spec8 := gpuv1beta1.GpuSpec{TimeSlicing: &gpuv1beta1.TimeSlicingSpec{Replicas: 8}}
		if TimeSlicingConfigMapName(spec4) == TimeSlicingConfigMapName(spec8) {
			t.Error("TimeSlicingConfigMapName() returned same name for different replica counts")
		}
	})

	t.Run("returns empty string when time-slicing is nil", func(t *testing.T) {
		if got := TimeSlicingConfigMapName(gpuv1beta1.GpuSpec{}); got != "" {
			t.Errorf("TimeSlicingConfigMapName() = %q, want \"\"", got)
		}
	})
}
