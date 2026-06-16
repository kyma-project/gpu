# Configuration

The `Gpu` custom resource is the single configuration surface. There is one
`Gpu` per cluster; it must be named `gpu`.

## Minimal configuration

The empty CR is valid. The operator picks reasonable defaults from the
embedded NVIDIA GPU Operator chart and Garden Linux values.

```yaml
apiVersion: gpu.kyma-project.io/v1beta1
kind: Gpu
metadata:
  name: gpu
```

## Spec reference

```yaml
apiVersion: gpu.kyma-project.io/v1beta1
kind: Gpu
metadata:
  name: gpu
spec:
  driver:
    # Optional. Override the NVIDIA driver version. If empty, the default for
    # the detected OS is used (Garden Linux: precompiled driver pinned in the
    # operator binary; Ubuntu: NVIDIA chart default).
    # Example: "580.126.20"
    version: ""
```

| Field | Type | Default | Description |
|---|---|---|---|
| `spec.driver.version` | string | empty (chart default) | NVIDIA driver version. Override only when you need a specific version for compatibility with a CUDA application. |

## Status reference

The operator writes the following:

```yaml
status:
  operatorVersion: v26.3.2          # Embedded NVIDIA chart version
  driver:
    version: "580.126.20"           # Driver version reported by the DaemonSet
    nodesReady: 3                   # GPU nodes with healthy drivers
  conditions:
    - type: Preflight
      status: "True"
      reason: Passed
      message: "All GPU nodes run a supported OS"
    - type: HelmInstalled
      status: "True"
      reason: Installed
      message: "NVIDIA chart applied at v26.3.2"
    - type: DriverReady
      status: "True"
      reason: Ready
      message: "3/3 driver pods ready"
    - type: ValidatorPassed
      status: "True"
      reason: Ready
      message: "ClusterPolicy state is ready; NVIDIA validator passed"
    - type: Ready
      status: "True"
      reason: Ready
      message: "GPU Operator is fully operational"
```

### Condition reasons

| Condition | Status | Reason | Meaning |
|---|---|---|---|
| Preflight | True | Passed | GPU nodes detected with supported OS |
| Preflight | Unknown | Waiting | No GPU nodes detected in cluster yet |
| Preflight | False | Failed | Unsupported OS or mixed OS across GPU nodes |
| HelmInstalled | True | Installed | Chart applied successfully |
| HelmInstalled | False | Failed | Helm install or upgrade failed |
| HelmInstalled | Unknown | Uninstalling | Helm release removal in progress (during deletion) |
| DriverReady | True | Ready | All driver DaemonSet pods are ready |
| DriverReady | Unknown | Waiting | Driver DaemonSet not yet present |
| DriverReady | Unknown | Progressing | Driver rollout in progress |
| DriverReady | False | ReadError | Failed to list driver DaemonSets |
| ValidatorPassed | True | Ready | NVIDIA `ClusterPolicy.status.state == "ready"` |
| ValidatorPassed | Unknown | Waiting | ClusterPolicy not yet observed |
| ValidatorPassed | Unknown | Progressing | ClusterPolicy state is `notReady` |
| ValidatorPassed | False | ReadError | Failed to read ClusterPolicy |
| WorkloadProtection | False | ActiveGPUWorkloads | Deletion blocked while GPU pods are active |
| Ready | True | Ready | All four input conditions are True |
| Ready | False | ForbiddenCRName | CR has a name other than `gpu` |

The `Ready` condition is computed from the four input conditions. See the
top-level [README](./README.md#status-conditions) for the full table.

## Singleton enforcement

`Gpu` is cluster-scoped. The CRD has a CEL admission rule:

```
self.metadata.name == 'gpu'
```

so attempts to create `Gpu` resources with other names are rejected at the
API server. The controller also enforces this at reconcile time.
