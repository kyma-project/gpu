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

import "context"

// Installer is the contract the controller uses to drive Helm operations.
// The concrete *Client talks to a real cluster; tests inject a fake.
type Installer interface {
	// InstallOrUpgrade installs or upgrades the GPU Operator chart with the given values.
	InstallOrUpgrade(ctx context.Context, chartData []byte, values map[string]interface{}) error

	// Uninstall removes the GPU Operator Helm release. It is idempotent: returns nil
	// if no release exists.
	Uninstall(ctx context.Context) error
}
