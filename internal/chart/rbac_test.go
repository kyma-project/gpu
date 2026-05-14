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

package chart

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	helmchart "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// chartGVK identifies a Kubernetes resource type by API group and kind.
type chartGVK struct {
	Group string
	Kind  string
}

func (g chartGVK) String() string {
	group := g.Group
	if group == "" {
		group = "core"
	}
	return fmt.Sprintf("%s/%s", group, g.Kind)
}

// grantedByRBAC lists every apiGroup + kind that the operator's ClusterRole permits.
// This must stay in sync with the kubebuilder:rbac markers in internal/controller/gpu_controller.go.
//
// Methodology follows the Operator SDK Helm plugin: render chart templates,
// extract all resource GVKs, grant RBAC for each.
//   - Chart templates: github.com/NVIDIA/gpu-operator/tree/main/deployments/gpu-operator/templates
//   - Helm release storage: helm.sh/docs/topics/rbac/
//   - Methodology: sdk.operatorframework.io/docs/building-operators/helm/tutorial/
//
// When this test fails after a chart version bump:
//  1. Add the missing entry to this map
//  2. Add a matching kubebuilder:rbac marker in internal/controller/gpu_controller.go
//  3. Run: make manifests
var grantedByRBAC = map[chartGVK]bool{
	// CRDs (from chart crds/ directory)
	{"apiextensions.k8s.io", "CustomResourceDefinition"}: true,

	// NVIDIA custom resources
	{"nvidia.com", "ClusterPolicy"}: true,
	{"nvidia.com", "NVIDIADriver"}:  true,

	// NFD custom resources
	{"nfd.k8s-sigs.io", "NodeFeatureRule"}: true,

	// RBAC
	{"rbac.authorization.k8s.io", "ClusterRole"}:        true,
	{"rbac.authorization.k8s.io", "ClusterRoleBinding"}: true,
	{"rbac.authorization.k8s.io", "Role"}:               true,
	{"rbac.authorization.k8s.io", "RoleBinding"}:        true,

	// Workloads
	{"apps", "Deployment"}: true,
	{"apps", "DaemonSet"}:  true,
	{"batch", "Job"}:       true,

	// Core resources
	{"", "ServiceAccount"}: true,
	{"", "ConfigMap"}:      true,

	// Policy
	{"policy", "PodDisruptionBudget"}: true,

	// Monitoring (conditional, only with Prometheus)
	{"monitoring.coreos.com", "PodMonitor"}: true,

	// OpenShift (conditional, only on OpenShift clusters)
	{"security.openshift.io", "SecurityContextConstraints"}: true,
}

var (
	reAPIVersion = regexp.MustCompile(`(?m)^apiVersion:\s*"?([^"\s]+)"?\s*$`)
	reKind       = regexp.MustCompile(`(?m)^kind:\s*"?([^"\s]+)"?\s*$`)
)

// TestChartResourcesCoveredByRBAC validates that every Kubernetes resource kind the
// embedded GPU Operator Helm chart can produce is covered by the operator's RBAC.
//
// It scans all chart templates (including subcharts like NFD) and CRDs,
// extracts every apiVersion/kind combination, and checks each against grantedByRBAC.
// Conditional templates (gated behind values flags) are included because the operator
// needs RBAC for any resource the chart *could* create, regardless of current values.
func TestChartResourcesCoveredByRBAC(t *testing.T) {
	data, err := GPUOperatorChart()
	if err != nil {
		t.Fatalf("loading embedded chart: %v", err)
	}

	chrt, err := loader.LoadArchive(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parsing chart archive: %v", err)
	}

	found := map[chartGVK]bool{}
	collectGVKs(chrt, found)

	// Check: every resource the chart produces must be in the RBAC allowlist.
	var uncovered []chartGVK
	for gvk := range found {
		if !grantedByRBAC[gvk] {
			uncovered = append(uncovered, gvk)
		}
	}

	if len(uncovered) > 0 {
		sort.Slice(uncovered, func(i, j int) bool {
			return uncovered[i].String() < uncovered[j].String()
		})
		t.Errorf("chart produces %d resource type(s) not covered by RBAC:", len(uncovered))
		for _, u := range uncovered {
			t.Errorf("  %s", u)
		}
		t.Error("add missing entries to grantedByRBAC in rbac_test.go and kubebuilder:rbac markers in gpu_controller.go, then run: make manifests")
	}

	// Fail on RBAC entries the chart no longer produces - stale permissions accumulate
	// silently after chart version bumps if not caught here.
	var stale []chartGVK
	for gvk := range grantedByRBAC {
		if !found[gvk] {
			stale = append(stale, gvk)
		}
	}
	if len(stale) > 0 {
		sort.Slice(stale, func(i, j int) bool {
			return stale[i].String() < stale[j].String()
		})
		t.Errorf("RBAC grants %d resource type(s) the chart no longer produces - remove stale entries from grantedByRBAC and the kubebuilder:rbac markers, then run: make manifests", len(stale))
		for _, s := range stale {
			t.Errorf("  stale: %s", s)
		}
	}

	// Log full resource inventory for audit visibility.
	all := make([]chartGVK, 0, len(found))
	for gvk := range found {
		all = append(all, gvk)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].String() < all[j].String() })
	t.Logf("chart produces %d resource type(s): %d covered, %d uncovered, %d stale:",
		len(found), len(found)-len(uncovered), len(uncovered), len(stale))
	for _, gvk := range all {
		t.Logf("  %s", gvk)
	}
}

// collectGVKs recursively extracts apiVersion/kind pairs from a chart and all its subcharts.
func collectGVKs(chrt *helmchart.Chart, found map[chartGVK]bool) {
	for _, tpl := range chrt.Templates {
		extractGVKs(tpl.Data, found)
	}
	for _, crd := range chrt.CRDObjects() {
		extractGVKs(crd.File.Data, found)
	}
	for _, dep := range chrt.Dependencies() {
		collectGVKs(dep, found)
	}
}

// extractGVKs parses raw YAML (possibly with Go template directives) and extracts
// apiVersion/kind pairs. Template expressions like {{ .Values.x }} are skipped.
//
// Splitting on "---" (with optional surrounding newlines) handles both file-start
// separators (--- at byte 0) and mid-file separators (\n---\n), preventing
// cross-document apiVersion/kind mismatches from multi-resource template files.
func extractGVKs(data []byte, found map[chartGVK]bool) {
	// Normalize line endings and split on YAML document boundaries.
	// bytes.Split on "\n---" misses a leading "---" at byte 0, so we strip a
	// leading "---\n" before splitting to handle both cases uniformly.
	trimmed := bytes.TrimPrefix(data, []byte("---\n"))
	for doc := range bytes.SplitSeq(trimmed, []byte("\n---")) {
		av := reAPIVersion.FindSubmatch(doc)
		k := reKind.FindSubmatch(doc)
		if av == nil || k == nil {
			continue
		}

		apiVersion := string(av[1])
		kind := string(k[1])

		if strings.Contains(apiVersion, "{{") || strings.Contains(kind, "{{") {
			continue
		}

		group := ""
		if parts := strings.SplitN(apiVersion, "/", 2); len(parts) == 2 {
			group = parts[0]
		}

		found[chartGVK{Group: group, Kind: kind}] = true
	}
}
