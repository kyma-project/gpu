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
	sigsyaml "sigs.k8s.io/yaml"
)

const nvidiaDriverRepo = "nvcr.io/nvidia"

// ClusterInfo captures what the operator has detected about the cluster.
// It is produced by the detection layer and consumed by BuildValues.
type ClusterInfo struct {
	// GardenLinux is true when all GPU nodes in the cluster run Garden Linux.
	GardenLinux bool
}

// BuildValues translates the Gpu CR spec and detected cluster information into
// a Helm values map for the NVIDIA GPU Operator chart.
//
// For Garden Linux clusters, the embedded gardenlinux.yaml is loaded as the base
// (pre-compiled driver, CDI, toolkit path, NFD config) and user spec overrides are
// applied on top. For other OS clusters, only spec overrides are applied.
func BuildValues(spec gpuv1beta1.GpuSpec, cluster ClusterInfo) (map[string]interface{}, error) {
	values := map[string]interface{}{}

	if cluster.GardenLinux {
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
func gardenLinuxBase() (map[string]interface{}, error) {
	raw, err := chart.GardenLinuxValues()
	if err != nil {
		return nil, fmt.Errorf("loading garden linux base values: %w", err)
	}
	var base map[string]interface{}
	if err := sigsyaml.Unmarshal(raw, &base); err != nil {
		return nil, fmt.Errorf("parsing garden linux base values: %w", err)
	}
	normalizeDriverVersion(base)
	return base, nil
}

// normalizeDriverVersion converts driver.version from float64 to string when the YAML
// value is a bare number (e.g. `version: 590` unmarshals as float64(590)).
func normalizeDriverVersion(values map[string]interface{}) {
	driver, _ := values["driver"].(map[string]interface{})
	if driver == nil {
		return
	}
	if f, ok := driver["version"].(float64); ok {
		driver["version"] = strconv.FormatInt(int64(f), 10)
	}
}

// applySpecOverrides applies user-driven overrides from the Gpu CR spec on top of
// whatever base values are already in the map.
func applySpecOverrides(values map[string]interface{}, spec gpuv1beta1.GpuSpec) {
	// get driver version from values (gardenlinux base or empty)
	driver, _ := values["driver"].(map[string]interface{})
	if driver == nil {
		driver = map[string]interface{}{
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
