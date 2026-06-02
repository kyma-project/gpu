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
	"strconv"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/internal/chart"
	"github.com/kyma-project/gpu/internal/detection"
	sigsyaml "sigs.k8s.io/yaml"
)

const nvidiaDriverRepo = "nvcr.io/nvidia"

// ClusterInfo captures what the operator has detected about the cluster.
// It is produced by the detection layer and consumed by BuildValues.
type ClusterInfo struct {
	// OS is the operating system running on all GPU nodes in the cluster.
	// Preflight guarantees all GPU nodes share the same OS before BuildValues is called.
	OS detection.OSType
}

// BuildValues translates the Gpu CR spec and detected cluster information into
// a Helm values map for the NVIDIA GPU Operator chart.
//
// For Garden Linux clusters, the embedded gardenlinux.yaml is loaded as the base
// (pre-compiled driver, CDI, toolkit path, NFD config) and user spec overrides are
// applied on top. For Ubuntu clusters, NVIDIA's defaults are used (standard driver
// image, compiled on node) with only spec overrides applied.
func BuildValues(spec gpuv1beta1.GpuSpec, cluster ClusterInfo) (map[string]any, error) {
	values := map[string]any{}

	if cluster.OS == detection.OSTypeGardenLinux {
		base, err := gardenLinuxBase()
		if err != nil {
			return nil, err
		}
		values = base
	}

	applySpecOverrides(values, spec)

	return values, nil
}

// gardenLinuxBase loads the embedded gardenlinux.yaml and returns it as a values map.
// sigs.k8s.io/yaml unmarshals bare numbers as float64, so driver.version (e.g. 590)
// is normalized to a string to keep the map type-consistent with spec overrides.
func gardenLinuxBase() (map[string]any, error) {
	raw, err := chart.GardenLinuxValues()
	if err != nil {
		return nil, fmt.Errorf("loading garden linux base values: %w", err)
	}
	var base map[string]any
	if err := sigsyaml.Unmarshal(raw, &base); err != nil {
		return nil, fmt.Errorf("parsing garden linux base values: %w", err)
	}
	normalizeDriverVersion(base)
	return base, nil
}

// normalizeDriverVersion converts driver.version from float64 to string when the YAML
// value is a bare number (e.g. `version: 590` unmarshals as float64(590)).
func normalizeDriverVersion(values map[string]any) {
	driver, _ := values["driver"].(map[string]any)
	if driver == nil {
		return
	}
	if f, ok := driver["version"].(float64); ok {
		driver["version"] = strconv.FormatInt(int64(f), 10)
	}
}

// applySpecOverrides applies user-driven overrides from the Gpu CR spec on top of
// whatever base values are already in the map.
func applySpecOverrides(values map[string]any, spec gpuv1beta1.GpuSpec) {
	// get driver version from values (gardenlinux base or empty)
	driver, _ := values["driver"].(map[string]any)
	if driver == nil {
		driver = map[string]any{
			"enabled":    true,
			"repository": nvidiaDriverRepo,
		}
	}

	// try to get driver version from user spec
	if v := specDriverVersion(spec); v != "" {
		driver["version"] = v
	}

	values["driver"] = driver
}

// specDriverVersion returns the driver version override from the spec, or empty string.
func specDriverVersion(spec gpuv1beta1.GpuSpec) string {
	if spec.Driver == nil {
		return ""
	}
	return spec.Driver.Version
}
