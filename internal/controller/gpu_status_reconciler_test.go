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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
)

var _ = Describe("GpuStatusReconciler", func() {
	const gpuName = "test-gpu-status"

	var (
		reconciler *GpuStatusReconciler
		req        reconcile.Request
	)

	BeforeEach(func() {
		reconciler = &GpuStatusReconciler{Client: k8sClient}
		req = reconcileRequest(gpuName)
	})

	AfterEach(func() {
		deleteGpu(gpuName)
		deleteAllDriverDaemonSets(gpuOperatorNamespace)
		deleteClusterPolicy(clusterPolicyName)
	})

	Describe("guard", func() {
		It("does nothing when HelmInstalled condition is absent", func() {
			newGpu(gpuName)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero(), "should not poll before Helm installs")

			// No conditions should have been written by the status reconciler.
			Expect(getCondition(gpuName, condDriverReady)).To(BeNil())
			Expect(getCondition(gpuName, condValidatorPassed)).To(BeNil())
		})

		It("does nothing when HelmInstalled=False", func() {
			newGpu(gpuName)
			setHelmInstalledFalse(gpuName)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(getCondition(gpuName, condDriverReady)).To(BeNil())
		})
	})

	Describe("driver DaemonSet", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			setHelmInstalledTrue(gpuName)
		})

		It("sets DriverReady=Unknown when the DaemonSet does not exist yet", func() {
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueWarn))

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonWaiting))
			Expect(cond.Message).To(ContainSubstring("not found"))
		})

		It("sets DriverReady=Unknown while pods are still rolling out", func() {
			createDriverDaemonSet(3, 1, 1, 1) // desired=3, only 1 ready/available/updated

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueWarn))

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonProgressing))
			Expect(cond.Message).To(ContainSubstring("1/3"))
		})

		It("sets DriverReady=Unknown when desired=0 (no GPU nodes)", func() {
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
			// Simulates a node pool with two kernel versions: 2 nodes each, both fully ready.
			createDriverDaemonSet(2, 2, 2, 2)
			createDriverDaemonSetNamed("nvidia-driver-daemonset-6.19.0-cloud-amd64", 2, 2, 2, 2)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(ContainSubstring("4/4"), "should aggregate totals across both DaemonSets")
		})

		It("sets DriverReady=Unknown while one of multiple DaemonSets is still rolling out", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createDriverDaemonSetNamed("nvidia-driver-daemonset-6.19.0-cloud-amd64", 2, 1, 1, 1)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			cond := getCondition(gpuName, condDriverReady)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonProgressing))
			Expect(cond.Message).To(ContainSubstring("3/4"), "should reflect aggregated ready/desired")
		})
	})

	Describe("ClusterPolicy", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			setHelmInstalledTrue(gpuName)
			createDriverDaemonSet(2, 2, 2, 2) // DriverReady=True, so Ready depends on validator
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
		It("sets Ready=True only when all four conditions are True", func() {
			newGpu(gpuName)
			setHelmInstalledTrue(gpuName)
			setPreflightTrue(gpuName)
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			readyCond := getCondition(gpuName, condReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal(reasonReady))
		})

		It("sets Ready=Unknown while driver is still rolling out", func() {
			newGpu(gpuName)
			setHelmInstalledTrue(gpuName)
			setPreflightTrue(gpuName)
			createDriverDaemonSet(3, 1, 1, 1)
			createClusterPolicy("ready")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			readyCond := getCondition(gpuName, condReady)
			Expect(readyCond.Status).To(Equal(metav1.ConditionUnknown))
		})
	})

	Describe("idempotency", func() {
		It("does not change conditions when NVIDIA state is unchanged on a second reconcile", func() {
			newGpu(gpuName)
			setHelmInstalledTrue(gpuName)
			setPreflightTrue(gpuName)
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			By("first reconcile - writes conditions")
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			rvAfterFirst := gpu.ResourceVersion

			By("second reconcile - nothing changed, no patch should be issued")
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.ResourceVersion).To(Equal(rvAfterFirst), "ResourceVersion must not change when nothing changed")
		})

		It("does not accumulate duplicate conditions on repeated reconciles", func() {
			newGpu(gpuName)
			setHelmInstalledTrue(gpuName)
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			for _, condType := range []string{condDriverReady, condValidatorPassed} {
				count := 0
				for _, c := range gpu.Status.Conditions {
					if c.Type == condType {
						count++
					}
				}
				Expect(count).To(Equal(1), "condition %s must appear exactly once", condType)
			}
		})
	})
})

// setHelmInstalledTrue patches the Gpu status to simulate GpuReconciler having
// completed a successful Helm install.
func setHelmInstalledTrue(gpuName string) {
	gpu := &gpuv1beta1.Gpu{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
	patch := gpu.DeepCopy()
	apimeta.SetStatusCondition(&gpu.Status.Conditions, metav1.Condition{
		Type:               condHelmInstalled,
		Status:             metav1.ConditionTrue,
		Reason:             reasonInstalled,
		Message:            "installed",
		ObservedGeneration: gpu.Generation,
	})
	Expect(k8sClient.Status().Patch(ctx, gpu, client.MergeFrom(patch))).To(Succeed())
}

// setHelmInstalledFalse patches the Gpu status to simulate a failed Helm install.
func setHelmInstalledFalse(gpuName string) {
	gpu := &gpuv1beta1.Gpu{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
	patch := gpu.DeepCopy()
	apimeta.SetStatusCondition(&gpu.Status.Conditions, metav1.Condition{
		Type:               condHelmInstalled,
		Status:             metav1.ConditionFalse,
		Reason:             reasonFailed,
		Message:            "helm failed",
		ObservedGeneration: gpu.Generation,
	})
	Expect(k8sClient.Status().Patch(ctx, gpu, client.MergeFrom(patch))).To(Succeed())
}

// setPreflightTrue patches the Gpu status to simulate a passed preflight check.
func setPreflightTrue(gpuName string) {
	gpu := &gpuv1beta1.Gpu{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
	patch := gpu.DeepCopy()
	apimeta.SetStatusCondition(&gpu.Status.Conditions, metav1.Condition{
		Type:               condPreflight,
		Status:             metav1.ConditionTrue,
		Reason:             reasonPassed,
		Message:            "passed",
		ObservedGeneration: gpu.Generation,
	})
	Expect(k8sClient.Status().Patch(ctx, gpu, client.MergeFrom(patch))).To(Succeed())
}

// createDriverDaemonSet creates the nvidia-driver-daemonset in gpu-operator namespace
// with the given status counters.
func createDriverDaemonSet(desired, ready, available, updated int32) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      driverAppLabel,
			Namespace: gpuOperatorNamespace,
			Labels:    map[string]string{"app": driverAppLabel},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nvidia-driver"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nvidia-driver"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "driver", Image: "nvcr.io/nvidia/driver:latest"}}},
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

// deleteDaemonSet removes a DaemonSet.
func deleteDaemonSet(name, namespace string) {
	ds := &appsv1.DaemonSet{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ds); err != nil {
		return
	}
	_ = k8sClient.Delete(ctx, ds)
}

// deleteAllDriverDaemonSets removes all DaemonSets with the driver app label in the given
// namespace. Used in AfterEach to guarantee cleanup even when a test fails before DeferCleanup
// is registered.
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

// createDriverDaemonSetNamed creates a driver DaemonSet with an explicit name but the
// same app label, simulating NVIDIA's per-kernel-version DaemonSet naming scheme.
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
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "driver", Image: "nvcr.io/nvidia/driver:latest"}}},
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

// createClusterPolicy creates an NVIDIA ClusterPolicy unstructured object with the
// given status.state value.
func createClusterPolicy(state string) {
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(clusterPolicyGVK)
	cp.SetName(clusterPolicyName)
	Expect(k8sClient.Create(ctx, cp)).To(Succeed())
	cp.Object["status"] = map[string]interface{}{"state": state}
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
