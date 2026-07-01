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

package setup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/yaml"

	gpucfg "github.com/kyma-project/gpu/tests/e2e/pkg/config"
	"github.com/kyma-project/gpu/tests/e2e/pkg/helpers/client"
)

const (
	baseDirEnvVariable = "E2E_LOGS_DIR"
	podLogsDir         = "pods"
	podLogFileName     = "%s-%s@%s.log"
)

var (
	logsTimeStamp = time.Now().Format("02_01_2006-15_04_05")
	basePath      = path.Join(".", "logs")
)

// DumpClusterResources writes the GPU-relevant resources (Gpu CR, gpu-operator
// namespace contents, ClusterPolicy, NVIDIA driver DaemonSets, Nodes) and pod
// logs from the gpu-operator namespace to disk for post-mortem analysis.
// All errors are swallowed and logged - dumping is best-effort.
func DumpClusterResources(t *testing.T) {
	t.Helper()
	if ws, ok := os.LookupEnv(baseDirEnvVariable); ok {
		basePath = path.Join(ws, "logs")
	}
	cfg := gpucfg.Get()
	dumpPath := path.Join(basePath, logsTimeStamp, t.Name(), "resources")
	if _, err := os.Stat(dumpPath); !os.IsNotExist(err) {
		return
	}
	if err := os.MkdirAll(dumpPath, 0o755); err != nil {
		t.Logf("Could not create dump directory: %v", err)
		return
	}

	r, err := client.ResourcesClient(t)
	if err != nil {
		t.Logf("Could not create resources client: %v", err)
		return
	}

	// Cluster-scoped resources.
	dumpResource(t, r, dumpPath, schema.GroupVersionKind{Group: "gpu.kyma-project.io", Version: "v1beta1", Kind: "GpuList"}, "")
	dumpResource(t, r, dumpPath, schema.GroupVersionKind{Group: "nvidia.com", Version: "v1", Kind: "ClusterPolicyList"}, "")
	dumpResource(t, r, dumpPath, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "NodeList"}, "")

	// gpu-operator namespace resources.
	dumpResource(t, r, dumpPath, schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DaemonSetList"}, cfg.GpuOperatorNamespace)
	dumpResource(t, r, dumpPath, schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DeploymentList"}, cfg.GpuOperatorNamespace)
	dumpResource(t, r, dumpPath, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "PodList"}, cfg.GpuOperatorNamespace)

	dumpPodLogs(t, cfg.GpuOperatorNamespace)
}

func dumpResource(t *testing.T, r *resources.Resources, dir string, gvk schema.GroupVersionKind, namespace string) {
	t.Helper()
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)

	// Resources.WithNamespace mutates the receiver; clone-effect by re-creating client per call would be expensive,
	// so use the controller-runtime client directly with the right ListOptions.
	crClient := r.GetControllerRuntimeClient()
	var opts []crclient.ListOption
	if namespace != "" {
		opts = append(opts, crclient.InNamespace(namespace))
	}
	listErr := crClient.List(context.Background(), list, opts...)
	if listErr != nil {
		t.Logf("Could not list %s: %v", gvk.Kind, listErr)
		return
	}

	suffix := ""
	if namespace != "" {
		suffix = "@" + namespace
	}
	fileName := path.Join(dir, gvk.Kind+suffix)
	data, err := yaml.Marshal(list)
	if err != nil {
		t.Logf("Could not marshal %s: %v", gvk.Kind, err)
		return
	}
	if err := os.WriteFile(fileName, data, 0o644); err != nil {
		t.Logf("Could not write %s: %v", gvk.Kind, err)
	}
}

func dumpPodLogs(t *testing.T, namespace string) {
	t.Helper()
	if namespace == "" {
		return
	}
	dir := path.Join(basePath, logsTimeStamp, t.Name(), podLogsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("Could not create log directory: %v", err)
		return
	}

	cs, err := client.GetClientSet(t)
	if err != nil {
		t.Logf("Could not get client set: %v", err)
		return
	}
	pods, err := cs.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Logf("Could not list pods in %s: %v", namespace, err)
		return
	}
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			stream, err := cs.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				Container:  container.Name,
				Timestamps: true,
			}).Stream(context.Background())
			if err != nil {
				t.Logf("Could not stream logs from %s/%s: %v", pod.Name, container.Name, err)
				continue
			}
			buf := &bytes.Buffer{}
			if _, err := buf.ReadFrom(stream); err != nil {
				t.Logf("Could not read logs from %s/%s: %v", pod.Name, container.Name, err)
				_ = stream.Close()
				continue
			}
			_ = stream.Close()
			fileName := path.Join(dir, fmt.Sprintf(podLogFileName, pod.Name, container.Name, namespace))
			if err := os.WriteFile(fileName, buf.Bytes(), 0o644); err != nil {
				t.Logf("Could not write logs %s: %v", fileName, err)
			}
		}
	}
}
