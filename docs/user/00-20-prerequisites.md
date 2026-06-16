# Prerequisites

Before enabling the GPU module on a cluster, confirm the following.

## GPU nodes

The cluster must have at least one node pool with a supported GPU instance
type. Detection is based on the `node.kubernetes.io/instance-type` label.

| Cloud provider | Supported prefixes |
|---|---|
| AWS | `g4dn`, `g6` |
| GCP | `g2-` |
| Azure | `Standard_NC` |

If your cluster has no GPU nodes, the operator surfaces `Preflight=Unknown` and
does not install the chart. Add a GPU node pool to your cluster spec
(Gardener `Shoot` or equivalent) before enabling the module.

## Operating system

All GPU nodes must run a supported OS. Mixed clusters (some Garden Linux, some
Ubuntu) are rejected by preflight.

| OS | Driver source |
|---|---|
| Garden Linux | Precompiled signed kernel modules from `ghcr.io/gardenlinux/gardenlinux-nvidia-installer` |
| Ubuntu | Runtime driver compilation by the NVIDIA driver DaemonSet |

**Recommendation:** use Garden Linux. Precompiled modules avoid the runtime
compilation step and reduce reconciliation time and failure surface.

## Cluster permissions

The operator runs in `gpu-system` and creates resources in `gpu-operator`. It
needs cluster-admin-equivalent permissions during install. Lifecycle-manager
applies the required RBAC when the module is enabled - no manual permission
setup is required.

## No manual driver installation

Do not install NVIDIA drivers manually on nodes. The module manages driver
lifecycle. Manual installation can conflict with the chart-deployed driver
DaemonSet.

## Single CR per cluster

The `Gpu` resource is a cluster-scoped singleton. The CR must be named `gpu`.
Other names are rejected by both a CEL admission rule on the CRD and by the
controller as defense in depth. There is no support for partial enablement
across namespaces - the module either runs cluster-wide or not at all.
