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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/internal/helm"
)

// fakeInstaller is a test double for helm.Installer.
// It records whether InstallOrUpgrade and Uninstall were called and can be
// configured to return errors.
type fakeInstaller struct {
	installCalls    int
	uninstallCalled bool
	installErr      error
	uninstallErr    error
}

func (f *fakeInstaller) InstallOrUpgrade(_ context.Context, _ []byte, _ map[string]interface{}) error {
	f.installCalls++
	return f.installErr
}

func (f *fakeInstaller) Uninstall(_ context.Context) error {
	f.uninstallCalled = true
	return f.uninstallErr
}

// compile-time check that fakeInstaller satisfies the interface
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
	})

	Describe("finalizer", func() {
		It("adds the finalizer on first reconcile without explicit requeue", func() {
			By("creating a Gpu CR with no finalizer")
			newGpu(gpuName)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("verifying the finalizer is present")
			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Finalizers).To(ContainElement(finalizer))
		})
	})

	Describe("preflight", func() {
		BeforeEach(func() {
			// Create the CR and trigger the first reconcile to add the finalizer,
			// so subsequent reconciles reach the preflight check.
			newGpu(gpuName)
			_, err := reconciler.Reconcile(ctx, req) // adds finalizer
			Expect(err).NotTo(HaveOccurred())
		})

		It("sets Preflight=Unknown when no GPU nodes are present", func() {
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueWarn), "should requeue to retry when nodes are absent")

			cond := getCondition(gpuName, condPreflight)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonWaiting))

			Expect(installer.installCalls).To(Equal(0), "Helm must not be called before preflight passes")
		})

		It("sets Preflight=False when GPU nodes run a non-Garden-Linux OS", func() {
			By("creating a GPU node with Ubuntu OS")
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

			By("creating a Garden Linux GPU node so preflight passes")
			newGpuNode("gpu-node-gl", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-gl")
		})

		It("calls Helm and sets HelmInstalled=True on success", func() {
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			Expect(installer.installCalls).To(Equal(1))

			preflight := getCondition(gpuName, condPreflight)
			Expect(preflight.Status).To(Equal(metav1.ConditionTrue))
			Expect(preflight.Reason).To(Equal(reasonPassed))

			helmCond := getCondition(gpuName, condHelmInstalled)
			Expect(helmCond).NotTo(BeNil())
			Expect(helmCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(helmCond.Reason).To(Equal(reasonInstalled))

			By("verifying operatorVersion is recorded in status")
			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.OperatorVersion).NotTo(BeEmpty())
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
		})

		It("records Ready=Unknown after a successful Helm install (pods not yet up)", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			readyCond := getCondition(gpuName, condReady)
			Expect(readyCond).NotTo(BeNil())
			// HelmInstalled=True but DriverReady/ValidatorPassed not yet set -> Unknown
			Expect(readyCond.Status).To(Equal(metav1.ConditionUnknown))
		})
	})

	Describe("deletion", func() {
		It("calls Helm uninstall and removes the finalizer", func() {
			By("creating the CR and bootstrapping through to HelmInstalled=True")
			newGpu(gpuName)
			_, _ = reconciler.Reconcile(ctx, req) // add finalizer
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

			By("verifying the finalizer has been removed")
			gpu = &gpuv1beta1.Gpu{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)
			// Object should be gone (finalizer removed -> garbage collected) or have no finalizer.
			if err == nil {
				Expect(gpu.Finalizers).NotTo(ContainElement(finalizer))
			}
		})
	})

	Describe("idempotency", func() {
		BeforeEach(func() {
			newGpu(gpuName)
			_, _ = reconciler.Reconcile(ctx, req) // add finalizer
			newGpuNode("gpu-node-idem", "g4dn.xlarge", "Garden Linux 1312.3")
			DeferCleanup(deleteNode, "gpu-node-idem")
		})

		It("does not accumulate duplicate conditions on repeated reconciles", func() {
			By("first reconcile - Helm install")
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(installer.installCalls).To(Equal(1))

			By("second reconcile - same state")
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			By("HelmInstalled condition must appear exactly once")
			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			count := 0
			for _, c := range gpu.Status.Conditions {
				if c.Type == condHelmInstalled {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})

		It("does not change operatorVersion on a second reconcile", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			gpu := &gpuv1beta1.Gpu{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			versionAfterFirst := gpu.Status.OperatorVersion
			Expect(versionAfterFirst).NotTo(BeEmpty())

			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gpuName}, gpu)).To(Succeed())
			Expect(gpu.Status.OperatorVersion).To(Equal(versionAfterFirst))
		})
	})
})
