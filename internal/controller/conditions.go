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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Condition types.
	condReady           = "Ready"
	condPreflight       = "Preflight"
	condHelmInstalled   = "HelmInstalled"
	condDriverReady     = "DriverReady"
	condValidatorPassed = "ValidatorPassed"

	// Condition reasons.
	reasonWaiting      = "Waiting"      // outcome not yet determined; controller is still watching
	reasonProgressing  = "Progressing"  // resource exists and is converging toward the desired state
	reasonReady        = "Ready"        // condition is fully met
	reasonPassed       = "Passed"       // one-shot check succeeded (e.g. preflight)
	reasonFailed       = "Failed"       // definitively failed; requires user action
	reasonInstalled    = "Installed"    // Helm release applied successfully
	reasonUninstalling = "Uninstalling" // Helm release removal in progress
	reasonReadError    = "ReadError"    // Kubernetes API read failed
)

// computeReadySummary derives the Ready summary condition from the four managed conditions.
// Priority: any False beats any Unknown beats all True.
// The reason and message are taken from the first condition that determines the outcome,
// in dependency order (Preflight -> HelmInstalled -> DriverReady -> ValidatorPassed).
func computeReadySummary(conditions []metav1.Condition, generation int64) metav1.Condition {
	managed := []string{condPreflight, condHelmInstalled, condDriverReady, condValidatorPassed}

	// First pass: any False is an immediate summary - something is definitively broken.
	for _, t := range managed {
		c := apimeta.FindStatusCondition(conditions, t)
		if c != nil && c.Status == metav1.ConditionFalse {
			return metav1.Condition{
				Type:               condReady,
				Status:             metav1.ConditionFalse,
				Reason:             c.Reason,
				Message:            c.Message,
				ObservedGeneration: generation,
			}
		}
	}

	// Second pass: any Unknown means still converging.
	for _, t := range managed {
		c := apimeta.FindStatusCondition(conditions, t)
		if c == nil || c.Status == metav1.ConditionUnknown {
			reason := reasonWaiting
			message := ""
			if c != nil {
				reason = c.Reason
				message = c.Message
			}
			return metav1.Condition{
				Type:               condReady,
				Status:             metav1.ConditionUnknown,
				Reason:             reason,
				Message:            message,
				ObservedGeneration: generation,
			}
		}
	}

	// All four are True.
	return metav1.Condition{
		Type:               condReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonReady,
		Message:            "GPU Operator is fully operational",
		ObservedGeneration: generation,
	}
}

// setCondition writes a condition entry, passing status, reason, and message directly so
// callers retain full control over the tri-state (True / False / Unknown).
func setCondition(conditions *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// conditionMatches returns true when the named condition already carries the given status, reason, and message.
// Used to skip a no-op status patch when nothing changed.
func conditionMatches(conditions []metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string) bool {
	c := apimeta.FindStatusCondition(conditions, condType)
	if c == nil {
		return false
	}
	return c.Status == status && c.Reason == reason && c.Message == message
}
