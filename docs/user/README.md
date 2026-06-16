# GPU Module

The GPU module enables NVIDIA GPU workloads on Kyma clusters. It manages the
[NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator) lifecycle through
a single cluster-scoped `Gpu` custom resource - install, upgrade, status, and
uninstall.

## Overview

When you enable the GPU module on a Kyma cluster that has GPU node pools, the
operator:

1. Detects the OS distribution running on GPU nodes (Garden Linux or Ubuntu).
2. Installs the NVIDIA GPU Operator Helm chart with values tuned for the
   detected OS - Garden Linux uses precompiled signed kernel modules; Ubuntu
   uses runtime driver compilation.
3. Reports installation, driver, and validator readiness through status
   conditions on the `Gpu` resource.
4. Cleans up all NVIDIA workloads on `Gpu` deletion, with a workload-protection
   guard that blocks deletion while pods are still using GPU resources.

The NVIDIA chart and Garden Linux driver values are embedded in the operator
binary, so reconciliation works in air-gapped clusters.

## Custom Resource

A single, cluster-scoped `Gpu` resource named `gpu` enables the module on a
cluster. Any other name is rejected at admission. The minimal CR is empty:

```yaml
apiVersion: gpu.kyma-project.io/v1beta1
kind: Gpu
metadata:
  name: gpu
```

See [00-40-configuration.md](./00-40-configuration.md) for the full spec.

## Status Conditions

The `Gpu` CR exposes the following conditions on `status.conditions`:

| Type | Meaning |
|---|---|
| `Preflight` | Cluster has supported GPU nodes with a supported OS |
| `HelmInstalled` | NVIDIA chart applied successfully |
| `DriverReady` | NVIDIA driver DaemonSets are rolled out on all GPU nodes |
| `ValidatorPassed` | NVIDIA validator job has completed successfully |
| `WorkloadProtection` | Set to `False` during deletion when GPU pods are still active, blocking uninstall |
| `Ready` | Computed summary: `True` only when all four input conditions are `True` |

`Ready=True` is the signal a workload can request `nvidia.com/gpu` resources.

## Documentation

- [Overview](./00-10-overview.md)
- [Prerequisites](./00-20-prerequisites.md)
- [Installation](./00-30-installation.md)
- [Configuration](./00-40-configuration.md)
- [Operations](./00-50-operations.md)
- [Limitations](./00-60-limitations.md)

## Feedback

File issues at https://github.com/kyma-project/gpu/issues. For security
concerns, see [SECURITY.md](../../SECURITY.md).
