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

// SSA concurrency tests verify that two controllers writing to the same
// Gpu CR status subresource via Server-Side Apply do not overwrite each
// other's conditions across interleaved reconcile cycles.
//
// Field ownership split:
//   - "gpu-controller"        → Preflight, HelmInstalled, operatorVersion
//   - "gpu-status-controller" → DriverReady, ValidatorPassed, Ready, driver
//
// Every spec here would FAIL if either controller used a plain MergeFrom patch
// (last-write-wins), and PASS with the current SSA implementation.

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("SSA concurrency", func() {
	const gpuName = "ssa-concurrency-gpu"
	const nodeName = "ssa-concurrency-node"

	var (
		installReconciler *GpuReconciler
		statusReconciler  *GpuStatusReconciler
		req               = reconcileRequest(gpuName)
	)

	BeforeEach(func() {
		installer := &fakeInstaller{}
		installReconciler = &GpuReconciler{Client: k8sClient, Installer: installer}
		statusReconciler = &GpuStatusReconciler{Client: k8sClient}

		newGpu(gpuName)
		DeferCleanup(deleteGpu, gpuName)

		// Bootstrap: add finalizer
		_, err := installReconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		// GPU node so preflight passes
		newGpuNode(nodeName, "g4dn.xlarge", "Garden Linux 1312.3")
		DeferCleanup(deleteNode, nodeName)

		DeferCleanup(deleteAllDriverDaemonSets, gpuOperatorNamespace)
		DeferCleanup(deleteClusterPolicy, clusterPolicyName)
	})

	// runInstall drives GpuReconciler through a full successful install cycle
	// (preflight + helm), setting Preflight=True and HelmInstalled=True.
	runInstall := func() {
		GinkgoHelper()
		_, err := installReconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
	}

	// runStatus drives GpuStatusReconciler with a fully-ready DaemonSet and
	// ClusterPolicy, setting DriverReady=True, ValidatorPassed=True, Ready=True.
	runStatus := func() {
		GinkgoHelper()
		_, err := statusReconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
	}

	// allFiveConditions asserts that all five managed condition types are present
	// on the Gpu CR and returns them for further assertions.
	allFiveConditions := func() map[string]*metav1.Condition {
		GinkgoHelper()
		m := map[string]*metav1.Condition{}
		for _, t := range []string{condPreflight, condHelmInstalled, condDriverReady, condValidatorPassed, condReady} {
			c := getCondition(gpuName, t)
			Expect(c).NotTo(BeNil(), "condition %q must be present", t)
			m[t] = c
		}
		return m
	}

	Describe("install controller conditions survive a status controller reconcile", func() {
		It("all 5 conditions present after install then status reconcile", func() {
			createDriverDaemonSet(3, 3, 3, 3)
			createClusterPolicy("ready")

			runInstall()
			runStatus()

			conds := allFiveConditions()
			Expect(conds[condPreflight].Status).To(Equal(metav1.ConditionTrue))
			Expect(conds[condHelmInstalled].Status).To(Equal(metav1.ConditionTrue))
			Expect(conds[condDriverReady].Status).To(Equal(metav1.ConditionTrue))
			Expect(conds[condValidatorPassed].Status).To(Equal(metav1.ConditionTrue))
			Expect(conds[condReady].Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Describe("status controller conditions survive a subsequent install controller reconcile", func() {
		It("status-owned conditions are not wiped when install reconciler re-runs", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			By("install then status: both sets of conditions written")
			runInstall()
			runStatus()
			allFiveConditions() // baseline assertion

			By("second install reconcile triggered by e.g. a node label change")
			runInstall()

			By("status-owned conditions must still be present")
			conds := allFiveConditions()
			Expect(conds[condDriverReady].Status).To(Equal(metav1.ConditionTrue),
				"DriverReady must survive a re-run of the install controller")
			Expect(conds[condValidatorPassed].Status).To(Equal(metav1.ConditionTrue),
				"ValidatorPassed must survive a re-run of the install controller")
			Expect(conds[condReady].Status).To(Equal(metav1.ConditionTrue),
				"Ready must survive a re-run of the install controller")
		})
	})

	Describe("install controller conditions survive a subsequent status controller reconcile", func() {
		It("install-owned conditions are not wiped when status reconciler re-runs", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			runInstall()
			runStatus()
			allFiveConditions()

			By("second status reconcile")
			runStatus()

			conds := allFiveConditions()
			Expect(conds[condPreflight].Status).To(Equal(metav1.ConditionTrue),
				"Preflight must survive a re-run of the status controller")
			Expect(conds[condHelmInstalled].Status).To(Equal(metav1.ConditionTrue),
				"HelmInstalled must survive a re-run of the status controller")
		})
	})

	Describe("interleaved reconciles produce no duplicate conditions", func() {
		It("each condition type appears exactly once after install→status→install→status", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			runInstall()
			runStatus()
			runInstall()
			runStatus()

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())

			// Exactly one entry per managed condition type - no duplicates.
			Expect(gpu.Status.Conditions).To(HaveLen(5),
				"expected exactly 5 conditions (one per type), got %d: %v",
				len(gpu.Status.Conditions), gpu.Status.Conditions)

			seen := map[string]int{}
			for _, c := range gpu.Status.Conditions {
				seen[c.Type]++
			}
			for _, t := range []string{condPreflight, condHelmInstalled, condDriverReady, condValidatorPassed, condReady} {
				Expect(seen[t]).To(Equal(1), "condition %q must appear exactly once", t)
			}
		})
	})

	Describe("no-op: status controller skips patch when state unchanged", func() {
		It("ResourceVersion is unchanged on a second reconcile with identical state", func() {
			// Full install first so HelmInstalled=True (required by status reconciler guard).
			runInstall()

			// DaemonSet absent → DriverReady=Unknown, reason=Waiting.
			By("first status reconcile writes DriverReady=Unknown")
			runStatus()
			cond := getCondition(gpuName, condDriverReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(reasonWaiting))

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			rv1 := gpu.ResourceVersion

			By("second reconcile with same state - must be a no-op")
			runStatus()
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.ResourceVersion).To(Equal(rv1),
				"ResourceVersion must not change when nothing changed")

			By("state changes: DaemonSet appears with 1/3 ready - reconcile must patch")
			createDriverDaemonSet(3, 1, 1, 1)
			runStatus()
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.ResourceVersion).NotTo(Equal(rv1),
				"ResourceVersion must change when DriverReady transitions to Progressing")

			cond = getCondition(gpuName, condDriverReady)
			Expect(cond.Reason).To(Equal(reasonProgressing))
		})
	})

	Describe("driver status field updated without clobbering conditions", func() {
		It("driver.version and nodesReady are set without wiping conditions", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			runInstall()
			runStatus()

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.Driver).NotTo(BeNil(), "driver status must be set")
			Expect(gpu.Status.Driver.NodesReady).To(Equal(int32(2)))

			By("install reconciler re-runs - must not zero out driver status")
			runInstall()

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.Driver).NotTo(BeNil(),
				"driver status must survive a re-run of the install controller")
			Expect(gpu.Status.Driver.NodesReady).To(Equal(int32(2)),
				"nodesReady must be preserved")

			// All conditions still present.
			allFiveConditions()
		})
	})

	Describe("Ready=True requires both controllers to have converged", func() {
		It("Ready is True only when all four input conditions are True", func() {
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			runInstall()

			By("after install only: Ready is not yet True - status controller hasn't run")
			readyCond := getCondition(gpuName, condReady)
			Expect(readyCond).To(BeNil(),
				"Ready must not be set before the status controller runs")

			By("after status controller runs: all inputs True → Ready=True")
			runStatus()

			readyCond = getCondition(gpuName, condReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("Ready=False when Preflight=False even if NVIDIA stack is healthy", func() {
			// Simulate a bad node OS that blocks install.
			// The install controller sets Preflight=False and returns without running Helm.
			// The status controller guard (HelmInstalled not True) means it won't run.
			// If somehow HelmInstalled were set manually, Ready must still be False.
			By("set HelmInstalled=True and DriverReady=True manually, but Preflight=False")
			setHelmInstalledTrue(gpuName)
			setPreflightFalse(gpuName)
			createDriverDaemonSet(2, 2, 2, 2)
			createClusterPolicy("ready")

			runStatus()

			readyCond := getCondition(gpuName, condReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse),
				"Ready must be False when Preflight=False, regardless of NVIDIA stack health")
		})
	})
})

// setPreflightFalse seeds Preflight=False via SSA under the install field owner.
func setPreflightFalse(gpuName string) {
	GinkgoHelper()
	gpu := &gpuv1beta1.Gpu{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
	cond := metav1.Condition{
		Type:               condPreflight,
		Status:             metav1.ConditionFalse,
		Reason:             reasonFailed,
		Message:            "GPU node is not running Garden Linux",
		ObservedGeneration: gpu.Generation,
		LastTransitionTime: metav1.Now(),
	}
	applyConditionSSA(gpuName, cond)
}
