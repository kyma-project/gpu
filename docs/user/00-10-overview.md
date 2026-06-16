# Overview

The GPU module installs and manages the NVIDIA GPU Operator on Kyma clusters.
It is the single integration point for running GPU workloads on SAP BTP, Kyma
runtime.

## What this module installs

When you create a `Gpu` resource, the operator installs the upstream NVIDIA GPU
Operator Helm chart, which in turn deploys:

- **NVIDIA driver DaemonSet** - kernel modules and user-space driver libraries.
- **Container Toolkit** - wires the NVIDIA runtime into containerd so pods can
  access `/dev/nvidia*`.
- **Device Plugin** - exposes `nvidia.com/gpu` as a schedulable resource to
  the kubelet.
- **DCGM and DCGM Exporter** - health and telemetry on port 9400 in
  Prometheus format.
- **Validator** - runs once after install to verify the stack works end-to-end.
- **Node Feature Discovery (NFD)** - labels nodes with hardware features.

## Architecture

The module is structured as a single controller (`GpuReconciler`) that
reconciles the cluster-scoped singleton `Gpu` CR. Reconcile runs through:

1. **Preflight** - validates GPU nodes exist and run a supported OS.
2. **Helm install/upgrade** - applies the embedded NVIDIA chart.
3. **Driver readiness** - watches the `nvidia-driver-daemonset` rollout.
4. **Validator** - reads `ClusterPolicy.status.state` from the NVIDIA CRD.
5. **Status summary** - computes `Ready` from the four input conditions
   (`Preflight`, `HelmInstalled`, `DriverReady`, `ValidatorPassed`). A separate
   `WorkloadProtection` condition is set during deletion if GPU pods are still
   active.

The chart is embedded in the operator binary via `//go:embed`, so the operator
does not need network access to NVIDIA's chart repository at reconcile time.

## When to use it

- You have GPU node pools (AWS `g4dn`/`g6`, GCP `g2-`, Azure `Standard_NC`) on
  a Kyma cluster.
- Your nodes run Garden Linux (the Kyma default) or Ubuntu.
- You want NVIDIA driver and runtime managed declaratively rather than
  installed manually per-node.

## When not to use it

- The cluster has no GPU nodes - install is a no-op but the operator still
  runs. Disable the module instead.
- Your nodes run an unsupported OS distribution. The preflight check rejects
  the install rather than attempting it. See
  [00-60-limitations.md](./00-60-limitations.md).
