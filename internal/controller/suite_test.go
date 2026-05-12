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

package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	Expect(gpuv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	if dir := firstEnvtestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("installing the ClusterPolicy CRD so status reconciler tests can create ClusterPolicy objects")
	Expect(k8sClient.Create(ctx, clusterPolicyCRD())).To(Succeed())

	By("creating the gpu-operator namespace used by status reconciler tests")
	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-operator"},
	})).To(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// newGpu creates a minimal cluster-scoped Gpu CR in the API server and returns it.
func newGpu(name string) *gpuv1beta1.Gpu {
	gpu := &gpuv1beta1.Gpu{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	Expect(k8sClient.Create(ctx, gpu)).To(Succeed())
	return gpu
}

// deleteGpu removes the Gpu CR, stripping any finalizer first so the object
// is fully gone before the test ends.
func deleteGpu(name string) {
	gpu := &gpuv1beta1.Gpu{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, gpu); err != nil {
		return // already gone
	}
	// Remove finalizer so deletion is not blocked.
	gpu.Finalizers = nil
	_ = k8sClient.Update(ctx, gpu)
	_ = k8sClient.Delete(ctx, gpu)
}

// newGpuNode creates a Node with the given instance-type label and OS image.
func newGpuNode(name, instanceType, osImage string) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"node.kubernetes.io/instance-type": instanceType,
			},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{OSImage: osImage},
		},
	}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	// envtest doesn't run the node lifecycle controller, so patch the status
	// sub-resource directly so NodeInfo is visible to the reconciler.
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
	return node
}

// deleteNode removes a Node from the API server.
func deleteNode(name string) {
	node := &corev1.Node{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, node); err != nil {
		return
	}
	_ = k8sClient.Delete(ctx, node)
}

// reconcileRequest builds a reconcile.Request for a cluster-scoped Gpu CR.
func reconcileRequest(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
}

// getCondition is a test helper that loads the Gpu CR and returns the named condition,
// or nil if it doesn't exist yet.
func getCondition(name, condType string) *metav1.Condition {
	gpu := &gpuv1beta1.Gpu{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, gpu); err != nil {
		return nil
	}
	return apimeta.FindStatusCondition(gpu.Status.Conditions, condType)
}

// firstEnvtestBinaryDir scans bin/k8s for the first envtest version directory so
// tests work when run from an IDE without KUBEBUILDER_ASSETS set.
func firstEnvtestBinaryDir() string {
	base := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			sub := filepath.Join(base, e.Name())
			inner, err := os.ReadDir(sub)
			if err != nil {
				continue
			}
			for _, i := range inner {
				if i.IsDir() {
					return filepath.Join(sub, i.Name())
				}
			}
			return sub
		}
	}
	return ""
}

// clusterPolicyCRD returns a minimal CRD definition for nvidia.com/v1 ClusterPolicy.
// envtest doesn't ship NVIDIA CRDs, so we install a stub so tests can create/get
// ClusterPolicy objects via the unstructured client (same as production code does).
func clusterPolicyCRD() *apiextensionsv1.CustomResourceDefinition {
	preserveUnknown := true
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "clusterpolicies.nvidia.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "nvidia.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "ClusterPolicy",
				ListKind: "ClusterPolicyList",
				Plural:   "clusterpolicies",
				Singular: "clusterpolicy",
			},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: &preserveUnknown,
						},
					},
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
				},
			},
		},
	}
}
