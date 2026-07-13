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
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
)

const (
	releaseName      = "gpu-operator"
	releaseNamespace = "gpu-operator"
)

// Client installs, upgrades, and uninstalls the NVIDIA GPU Operator Helm chart.
type Client struct {
	restConfig *rest.Config
}

// NewClient creates a Client that uses the given REST config to talk to the cluster.
func NewClient(cfg *rest.Config) *Client {
	return &Client{restConfig: cfg}
}

// InstallOrUpgrade installs the chart if no release exists, or upgrades it if one does.
// It returns changed=false when a deployed release already matches the desired chart
// version and values, skipping the upgrade entirely - without this, the reconciler's
// 30s requeue would drive a real Helm upgrade every cycle, churning hook Jobs and
// writing an unbounded stream of release-history Secrets into etcd.
//
// It always returns immediately without waiting for pods to become ready - the reconciler
// sets status to Processing and checks health on the next cycle.
func (c *Client) InstallOrUpgrade(ctx context.Context, chartData []byte, values map[string]any) (bool, error) {
	cfg, err := c.actionConfig()
	if err != nil {
		return false, err
	}

	if err := recoverPending(cfg); err != nil {
		return false, err
	}

	chrt, err := loadChart(chartData)
	if err != nil {
		return false, err
	}

	rel, err := currentRelease(cfg)
	if err != nil {
		return false, err
	}

	if rel == nil {
		if err := install(ctx, cfg, chrt, values); err != nil {
			return false, err
		}
		return true, nil
	}

	if releaseUpToDate(rel, chrt, values) {
		return false, nil
	}

	if err := upgrade(ctx, cfg, chrt, values); err != nil {
		return false, err
	}
	return true, nil
}

// releaseUpToDate reports whether the deployed release already matches the desired
// chart version and values, meaning an upgrade would be a no-op. It only returns true
// for a release in the deployed state - a failed release must always be re-upgraded so
// it can self-heal.
func releaseUpToDate(rel *release.Release, chrt *chart.Chart, values map[string]any) bool {
	if rel.Info == nil || rel.Info.Status != release.StatusDeployed {
		return false
	}
	if rel.Chart == nil || rel.Chart.Metadata == nil || rel.Chart.Metadata.Version != chrt.Metadata.Version {
		return false
	}
	return valuesEqual(rel.Config, values)
}

// valuesEqual compares two values maps by canonical JSON. Round-tripping through JSON
// normalizes the numeric-type differences (float64 vs int) that arise when Helm decodes
// stored release config versus values built in Go, so semantically identical maps compare
// equal. On any marshal error it returns false, forcing a real upgrade rather than risking
// a skipped one.
func valuesEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

// Uninstall removes the NVIDIA GPU Operator Helm release without waiting for
// pods to terminate. Pod cleanup is handled by the caller via foreground
// namespace deletion, which blocks until all child resources are gone before
// the namespace itself disappears. Waiting here as well caused Helm to block
// for 15+ minutes on real GPU hardware (driver pods take that long to
// terminate), and Helm's u.Timeout was not reliably honored.
func (c *Client) Uninstall(_ context.Context) error {
	cfg, err := c.actionConfig()
	if err != nil {
		return err
	}

	rel, err := currentRelease(cfg)
	if err != nil {
		return err
	}
	if rel == nil {
		return nil
	}

	u := action.NewUninstall(cfg)
	u.Wait = false
	if _, err := u.Run(releaseName); err != nil {
		return fmt.Errorf("uninstalling gpu-operator: %w", err)
	}
	return nil
}

// InstalledVersion returns the chart version of the current release, or empty string
// if no release exists. The reconciler uses this to detect when the embedded chart
// version has changed and an upgrade is needed.
func (c *Client) InstalledVersion() (string, error) {
	cfg, err := c.actionConfig()
	if err != nil {
		return "", err
	}
	rel, err := currentRelease(cfg)
	if err != nil {
		return "", err
	}
	if rel == nil {
		return "", nil
	}
	return rel.Chart.Metadata.Version, nil
}

// actionConfig prepares Helm to operate on the gpu-operator namespace by wiring up
// the cluster connection, release storage, and namespace scope - after this call,
// install, upgrade, uninstall, and list operations are ready to run.
func (c *Client) actionConfig() (*action.Configuration, error) {
	getter := newRESTClientGetter(c.restConfig, releaseNamespace)
	cfg := &action.Configuration{}
	if err := cfg.Init(getter, releaseNamespace, "secret", noopLog); err != nil {
		return nil, fmt.Errorf("initialising helm action config: %w", err)
	}
	return cfg, nil
}

// recoverPending detects and recovers releases stuck in pending-install or pending-upgrade,
// which happen when the operator crashes mid-operation. Without recovery, subsequent
// install/upgrade calls fail immediately.
func recoverPending(cfg *action.Configuration) error {
	list := action.NewList(cfg)
	list.StateMask = action.ListPendingInstall | action.ListPendingUpgrade
	releases, err := list.Run()
	if err != nil {
		return fmt.Errorf("listing pending releases: %w", err)
	}

	for _, r := range releases {
		// only act on our release named "gpu-operator"
		if r.Name != releaseName {
			continue
		}
		switch r.Info.Status {
		case release.StatusPendingUpgrade:
			rb := action.NewRollback(cfg)
			if err := rb.Run(releaseName); err != nil {
				return fmt.Errorf("rolling back pending-upgrade release: %w", err)
			}
		case release.StatusPendingInstall:
			u := action.NewUninstall(cfg)
			u.Wait = false
			if _, err := u.Run(releaseName); err != nil {
				return fmt.Errorf("removing pending-install release: %w", err)
			}
		}
	}
	return nil
}

// currentRelease returns the current deployed or failed release, or nil if none exists.
func currentRelease(cfg *action.Configuration) (*release.Release, error) {
	list := action.NewList(cfg)
	list.StateMask = action.ListDeployed | action.ListFailed
	releases, err := list.Run()
	if err != nil {
		return nil, fmt.Errorf("listing helm releases: %w", err)
	}
	for _, r := range releases {
		if r.Name == releaseName {
			return r, nil
		}
	}
	return nil, nil
}

func install(ctx context.Context, cfg *action.Configuration, chrt *chart.Chart, values map[string]any) error {
	act := action.NewInstall(cfg)
	act.ReleaseName = releaseName
	act.Namespace = releaseNamespace
	act.CreateNamespace = true
	act.Wait = false
	if _, err := act.RunWithContext(ctx, chrt, values); err != nil {
		return fmt.Errorf("installing gpu-operator: %w", err)
	}
	return nil
}

// maxReleaseHistory bounds the number of release-revision Secrets Helm retains.
// Helm's SDK default is 0 (unlimited); leaving it unbounded lets etcd grow without
// limit as revisions accumulate.
const maxReleaseHistory = 3

func upgrade(ctx context.Context, cfg *action.Configuration, chrt *chart.Chart, values map[string]any) error {
	act := action.NewUpgrade(cfg)
	act.Namespace = releaseNamespace
	act.Wait = false
	act.MaxHistory = maxReleaseHistory
	if _, err := act.RunWithContext(ctx, releaseName, chrt, values); err != nil {
		return fmt.Errorf("upgrading gpu-operator: %w", err)
	}
	return nil
}

func loadChart(data []byte) (*chart.Chart, error) {
	chrt, err := loader.LoadArchive(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("loading chart archive: %w", err)
	}
	return chrt, nil
}

// newRESTClientGetter wraps a *rest.Config so Helm's action.Configuration can use it.
func newRESTClientGetter(cfg *rest.Config, namespace string) genericclioptions.RESTClientGetter {
	return &restConfigGetter{cfg: cfg, namespace: namespace}
}

func noopLog(_ string, _ ...any) {
	// intentionally empty - suppresses Helm's internal debug output
}
