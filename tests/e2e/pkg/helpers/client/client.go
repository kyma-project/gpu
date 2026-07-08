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

// Package client wires the kubeconfig-driven Kubernetes clients used by
// the e2e suite. ResourcesClient returns a controller-runtime-backed client
// with the Gpu scheme registered; GetClientSet returns the typed client-go
// clientset for things like pod log streaming.
package client

import (
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/klient/conf"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/envconf"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
)

// KubeConfig returns the rest.Config resolved from $KUBECONFIG (or the
// default kubeconfig path). Fails the test if it cannot be read.
func KubeConfig(t *testing.T) *rest.Config {
	t.Helper()
	path := conf.ResolveKubeConfigFile()
	cfg := envconf.NewWithKubeConfig(path)
	return cfg.Client().RESTConfig()
}

// ResourcesClient returns a *resources.Resources backed by the kubeconfig
// from the environment, with the Gpu scheme registered on every returned instance.
func ResourcesClient(t *testing.T) (*resources.Resources, error) {
	t.Helper()
	r, err := resources.New(KubeConfig(t))
	if err != nil {
		t.Logf("Failed to create resources client: %v", err)
		return nil, err
	}

	if err := gpuv1beta1.AddToScheme(r.GetScheme()); err != nil {
		t.Logf("Failed to add gpu v1beta1 scheme: %v", err)
		return nil, err
	}

	return r, nil
}

// GetClientSet returns a typed client-go clientset. Used for operations the
// controller-runtime client doesn't support cleanly (e.g. pod log streaming).
func GetClientSet(t *testing.T) (*kubernetes.Clientset, error) {
	t.Helper()
	return kubernetes.NewForConfig(KubeConfig(t))
}
