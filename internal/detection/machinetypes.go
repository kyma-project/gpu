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

import "strings"

// instanceTypePrefixes lists the instance type label prefixes that indicate a GPU node
// per cloud provider. These are set by Gardener on every node before anything is installed,
// making them the only reliable signal on a fresh cluster.
//
// Only instance types offered via Kyma are listed here, not every GPU instance type the cloud provider offers.
var instanceTypePrefixes = []string{
	// AWS GPU families. Only G4dn and G6 are offered via Kyma
	"g4dn", "g6",
	// GCP GPU families. Only G2 series is offered via Kyma (g2-standard-*)
	"g2-",
	// Azure GPU families. Only NC series is offered via Kyma (Standard_NC*as_T4_v3)
	"Standard_NC",
}

// Label key that Gardener sets on every node at registration time
const instanceTypeLabel = "node.kubernetes.io/instance-type"

// IsGPUNode returns true if the node's instance type label matches a known GPU instance
// type prefix. Returns false if the label is absent or does not match any GPU prefix.
func IsGPUNode(labels map[string]string) bool {
	instanceType, ok := labels[instanceTypeLabel]
	if !ok || instanceType == "" {
		return false
	}
	for _, prefix := range instanceTypePrefixes {
		if strings.HasPrefix(instanceType, prefix) {
			return true
		}
	}
	return false
}
