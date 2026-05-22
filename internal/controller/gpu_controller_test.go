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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/internal/helm"
)

// fakeInstaller is a test double for helm.Installer. It records whether
// InstallOrUpgrade and Uninstall were called and can be configured to fail.
type fakeInstaller struct {
	installCalls    int
	uninstallCalled bool
	installErr      error
	uninstallErr    error
}

func (f *fakeInstaller) InstallOrUpgrade(_ context.Context, _ []byte, _ map[string]any) error {
	f.installCalls++
	return f.installErr
}

func (f *fakeInstaller) Uninstall(_ context.Context) error {
	f.uninstallCalled = true
	return f.uninstallErr
}

var _ helm.Installer = &fakeInstaller{}

var _ = Describe("GpuReconciler", func() {
	const gpuName = "test-gpu"

	var (
		reconciler *GpuReconciler
		installer  *fakeInstaller
		req        reconcile.Request
	)

	BeforeEach(func() {
		installer = &fakeInstaller{}
		reconciler = &GpuReconciler{
			Client:    k8sClient,
			Installer: installer,
		}
		req = reconcileRequest(gpuName)
	})

	AfterEach(func() {
		deleteGpu(gpuName)
		deleteAllDriverDaemonSets(gpuOperatorNamespace)
		deleteClusterPolicy(clusterPolicyName)
	})

	Describe("finalizer", func() {
		It("adds the finalizer on first reconcile without explicit requeue", func() {
			newGpu(gpuName)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Finalizers).To(ContainElement(finalizer))
			Expect(installer.installCalls).To(Equal(0))
		})
	})

	Describe("preflight", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			_, err := reconciler.Reconcile(ctx, req) // adds finalizer
			Expect(err).NotTo(HaveOccurred())
		})

		It("sets Preflight=Unknown and requeues when no GPU nodes are present", func() {
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueWarn))

			cond := getCondition(gpuName, condPreflight)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonWaiting))

			Expect(installer.installCalls).To(Equal(0), "Helm must not be called before preflight passes")
			// Ready summary is not written until Helm succeeds.
			Expect(getCondition(gpuName, condReady)).To(BeNil())
		})

		It("sets Preflight=False without requeue when a GPU node runs a non-Garden-Linux OS", func() {
			newGpuNode("gpu-node-ubuntu", "g4dn.xlarge", "Ubuntu 22.04")
			DeferCleanup(deleteNode, "gpu-node-ubuntu")

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero(), "should not requeue on a hard preflight error")

			cond := getCondition(gpuName, condPreflight)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonFailed))
			Expect(cond.Message).To(ContainSubstring("not running Garden Linux"))

			Expect(installer.installCalls).To(Equal(0))
		})
	})

	Describe("helm install", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			_, err := reconciler.Reconcile(ctx, req) // adds finalizer
			Expect(err).NotTo(HaveOccurred())

			newGpuNode("gpu-node-gl", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-gl")
		})

		It("installs and returns RequeueAfter for status convergence", func() {
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueWarn),
				"after a successful install, the reconciler must keep polling for NVIDIA stack convergence")

			Expect(installer.installCalls).To(Equal(1))

			Expect(getCondition(gpuName, condPreflight).Status).To(Equal(metav1.ConditionTrue))
			Expect(getCondition(gpuName, condHelmInstalled).Status).To(Equal(metav1.ConditionTrue))

			By("operatorVersion is recorded in status")
			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.OperatorVersion).NotTo(BeEmpty())
		})

		It("sets Ready=Unknown after a successful install when NVIDIA stack is still converging", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			readyCond := getCondition(gpuName, condReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionUnknown),
				"DriverReady and ValidatorPassed are Unknown until NVIDIA resources appear")

			driverCond := getCondition(gpuName, condDriverReady)
			Expect(driverCond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(driverCond.Reason).To(Equal(reasonWaiting))

			validatorCond := getCondition(gpuName, condValidatorPassed)
			Expect(validatorCond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(validatorCond.Reason).To(Equal(reasonWaiting))
		})

		It("sets HelmInstalled=False and returns an error when Helm fails", func() {
			installer.installErr = errors.New("simulated helm timeout")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("helm install/upgrade"))

			helmCond := getCondition(gpuName, condHelmInstalled)
			Expect(helmCond).NotTo(BeNil())
			Expect(helmCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(helmCond.Reason).To(Equal(reasonFailed))

			// We don't write Ready when Helm fails - downstream conditions are not meaningful yet.
			Expect(getCondition(gpuName, condReady)).To(BeNil())
		})
	})

	Describe("driver DaemonSet", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			_, err := reconciler.Reconcile(ctx, req) // adds finalizer
			Expect(err).NotTo(HaveOccurred())
			newGpuNode("gpu-node-ds", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-ds")
		})

		It("sets DriverReady=Unknown while pods are still rolling out", func() {
			createDriverDaemonSet(3, 1, 1, 1)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonProgressing))
			Expect(cond.Message).To(ContainSubstring("1/3"))
		})

		It("sets DriverReady=Unknown when desired=0 (no GPU nodes scheduled yet)", func() {
			createDriverDaemonSet(0, 0, 0, 0)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Message).To(ContainSubstring("no GPU nodes"))
		})

		It("sets DriverReady=True when all nodes are ready, available, and updated", func() {
			createDriverDaemonSet(3, 3, 3, 3)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonReady))
			Expect(cond.Message).To(ContainSubstring("3/3"))
		})

		It("aggregates across multiple DaemonSets (mixed kernel versions)", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createDriverDaemonSetNamed("nvidia-driver-daemonset-6.19.0-cloud-amd64", 2, 2, 2, 2)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(ContainSubstring("4/4"))
		})

		It("sets DriverReady=Unknown while one of multiple DaemonSets is still rolling out", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createDriverDaemonSetNamed("nvidia-driver-daemonset-6.19.0-cloud-amd64", 2, 1, 1, 1)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonProgressing))
			Expect(cond.Message).To(ContainSubstring("3/4"))
		})

		It("populates driver.version and driver.nodesReady from the DaemonSet", func() {
			createDriverDaemonSet(2, 2, 2, 2)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.Driver).NotTo(BeNil())
			Expect(gpu.Status.Driver.NodesReady).To(Equal(int32(2)))
		})

		It("clears stale driver fields when the DaemonSet disappears", func() {
			By("first reconcile populates driver.nodesReady from a healthy DS")
			createDriverDaemonSet(3, 3, 3, 3)
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.Driver).NotTo(BeNil())
			Expect(gpu.Status.Driver.NodesReady).To(Equal(int32(3)))

			By("DaemonSet is removed (e.g. mid-reinstall)")
			deleteAllDriverDaemonSets(gpuOperatorNamespace)

			By("next reconcile must clear stale nodesReady/version")
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.Driver).NotTo(BeNil())
			Expect(gpu.Status.Driver.NodesReady).To(BeZero(),
				"stale nodesReady from prior healthy read must be cleared when DS disappears")
			Expect(gpu.Status.Driver.Version).To(BeEmpty())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		})
	})

	Describe("ClusterPolicy", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			_, err := reconciler.Reconcile(ctx, req) // adds finalizer
			Expect(err).NotTo(HaveOccurred())
			newGpuNode("gpu-node-cp", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-cp")
			createDriverDaemonSet(2, 2, 2, 2)
		})

		It("sets ValidatorPassed=Unknown when ClusterPolicy does not exist yet", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condValidatorPassed)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonWaiting))
		})

		It("sets ValidatorPassed=Unknown when ClusterPolicy state is notReady", func() {
			createClusterPolicy("notReady")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condValidatorPassed)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonProgressing))
		})

		It("sets ValidatorPassed=True when ClusterPolicy state is ready", func() {
			createClusterPolicy("ready")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condValidatorPassed)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonReady))
		})
	})

	Describe("Ready summary", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			_, err := reconciler.Reconcile(ctx, req) // adds finalizer
			Expect(err).NotTo(HaveOccurred())
			newGpuNode("gpu-node-ready", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-ready")
		})

		It("Ready=True only when all four input conditions are True", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			readyCond := getCondition(gpuName, condReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal(reasonReady))
		})

		It("Ready=Unknown while driver is still rolling out", func() {
			createDriverDaemonSet(3, 1, 1, 1)
			createClusterPolicy("ready")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			Expect(getCondition(gpuName, condReady).Status).To(Equal(metav1.ConditionUnknown))
		})
	})

	Describe("idempotency", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			_, err := reconciler.Reconcile(ctx, req) // adds finalizer
			Expect(err).NotTo(HaveOccurred())
			newGpuNode("gpu-node-idem", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-idem")
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")
		})

		It("does not accumulate duplicate conditions across multiple reconciles", func() {
			for range 3 {
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
			}

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.Conditions).To(HaveLen(5),
				"expected exactly 5 conditions (one per type), got %d", len(gpu.Status.Conditions))

			seen := map[string]int{}
			for _, c := range gpu.Status.Conditions {
				seen[c.Type]++
			}
			for _, t := range []string{condPreflight, condHelmInstalled, condDriverReady, condValidatorPassed, condReady} {
				Expect(seen[t]).To(Equal(1), "condition %q must appear exactly once", t)
			}
		})

		It("skips the API write when nothing changed (ResourceVersion stable)", func() {
			By("first reconcile - converges to all True")
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			rv := gpu.ResourceVersion

			By("second reconcile with identical state must not bump ResourceVersion")
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.ResourceVersion).To(Equal(rv))
		})

		It("invokes Helm once per reconcile but does not duplicate operatorVersion writes", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			versionAfterFirst := gpu.Status.OperatorVersion
			Expect(versionAfterFirst).NotTo(BeEmpty())
			Expect(installer.installCalls).To(Equal(1))

			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.OperatorVersion).To(Equal(versionAfterFirst))
			Expect(installer.installCalls).To(Equal(2))
		})
	})

	Describe("deletion", func() {
		It("calls Helm uninstall and removes the finalizer", func() {
			newGpu(gpuName)
			_, _ = reconciler.Reconcile(ctx, req) // adds finalizer
			newGpuNode("gpu-node-del", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-del")
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(installer.installCalls).To(Equal(1))

			By("deleting the CR")
			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(k8sClient.Delete(ctx, gpu)).To(Succeed())

			By("reconciling the deletion")
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(installer.uninstallCalled).To(BeTrue())

			gpu = &gpuv1beta1.Gpu{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)
			if err == nil {
				Expect(gpu.Finalizers).NotTo(ContainElement(finalizer))
			}
		})
	})
})

// createDriverDaemonSet creates the nvidia-driver-daemonset in gpu-operator namespace
// with the given status counters.
func createDriverDaemonSet(desired, ready, available, updated int32) {
	createDriverDaemonSetNamed(driverAppLabel, desired, ready, available, updated)
}

// createDriverDaemonSetNamed creates a driver DaemonSet with an explicit name but the
// canonical app label, simulating NVIDIA's per-kernel-version DaemonSet naming scheme.
func createDriverDaemonSetNamed(name string, desired, ready, available, updated int32) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: gpuOperatorNamespace,
			Labels:    map[string]string{"app": driverAppLabel},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "driver", Image: "nvcr.io/nvidia/driver:535.129.03-gardenlinux"}}},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ds)).To(Succeed())
	ds.Status = appsv1.DaemonSetStatus{
		DesiredNumberScheduled: desired,
		NumberReady:            ready,
		NumberAvailable:        available,
		UpdatedNumberScheduled: updated,
	}
	Expect(k8sClient.Status().Update(ctx, ds)).To(Succeed())
}

// deleteAllDriverDaemonSets removes all driver DaemonSets in the given namespace, used
// in AfterEach to guarantee cleanup even when a test fails before DeferCleanup registers.
func deleteAllDriverDaemonSets(namespace string) {
	dsList := &appsv1.DaemonSetList{}
	if err := k8sClient.List(ctx, dsList,
		client.InNamespace(namespace),
		client.MatchingLabels{"app": driverAppLabel},
	); err != nil {
		return
	}
	for i := range dsList.Items {
		_ = k8sClient.Delete(ctx, &dsList.Items[i])
	}
}

// createClusterPolicy creates an NVIDIA ClusterPolicy unstructured object with the
// given status.state value.
func createClusterPolicy(state string) {
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(clusterPolicyGVK)
	cp.SetName(clusterPolicyName)
	Expect(k8sClient.Create(ctx, cp)).To(Succeed())
	cp.Object["status"] = map[string]any{"state": state}
	Expect(k8sClient.Status().Update(ctx, cp)).To(Succeed())
}

// deleteClusterPolicy removes the ClusterPolicy unstructured object.
func deleteClusterPolicy(name string) {
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(clusterPolicyGVK)
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, cp); err != nil {
		return
	}
	_ = k8sClient.Delete(ctx, cp)
}
