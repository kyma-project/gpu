# Operations

This page covers day-2 tasks: monitoring, upgrading, and triaging failures.

## Monitoring

### Inspecting status

```bash
kubectl get gpu
kubectl describe gpu gpu
```

`kubectl describe` lists every condition with reason and message - start
here when something is wrong.

### Operator metrics

The operator exposes its own metrics on `gpu-controller-manager-metrics-service`
in `gpu-system`, port 8443, secured via the controller-runtime metrics filter.
Kustomize bases for a `ServiceMonitor` (`config/prometheus/`) and a
`NetworkPolicy` (`config/network-policy/`) are present in the repo but are
**not** wired into the default overlay - apply them yourself if your cluster
runs Prometheus and you want metric scraping. The shipped `install.yaml`
contains only the CRD, RBAC, and manager Deployment.

### NVIDIA workload metrics

The DCGM exporter deployed by the NVIDIA chart exposes GPU telemetry on port
9400 in the `gpu-operator` namespace. The chart does **not** add
`prometheus.io/scrape` annotations - to scrape DCGM, deploy a `ServiceMonitor`
(or `PodMonitor`) targeting the `nvidia-dcgm-exporter` Service, or, in Kyma
clusters with the telemetry module enabled, create a `MetricPipeline` that
includes `gpu-operator` in `input.prometheus.namespaces.include`.

## Upgrading

Module upgrades go through lifecycle-manager. Bumping the channel or version
on the `Kyma` CR triggers an upgrade:

```bash
kubectl edit kyma <name> -n kcp-system
```

Lifecycle-manager replaces the operator Deployment with the new version. The
new operator runs `helm upgrade` on the next reconcile, which applies any
chart changes (new NVIDIA images, updated driver version, etc.).

`Ready` may transition through `Unknown` during the upgrade - this is
expected. If `Ready` does not return to `True` within ~10 minutes, see
[Triage](#triage) below.

## Driver version override

To pin a specific NVIDIA driver version:

```yaml
spec:
  driver:
    version: "580.126.20"
```

The operator runs `helm upgrade` with the new version. The driver DaemonSet
re-rolls. `DriverReady` transitions to `Unknown` during the rollout and back
to `True` when complete.

**Caveat for Garden Linux:** the Garden Linux driver image only ships specific
driver versions matched to specific kernel versions. Setting an arbitrary
version that has no precompiled artifact will fail at DaemonSet image pull.
Check the available tags at
`ghcr.io/gardenlinux/gardenlinux-nvidia-installer` before overriding.

## Triage

| Symptom | First check | Likely cause |
|---|---|---|
| `Preflight=False` | `kubectl describe gpu gpu` reason | Unsupported OS, or mixed OS across GPU nodes |
| `Preflight=Unknown` for a long time | `kubectl get nodes -l node.kubernetes.io/instance-type` | Cluster has no GPU nodes yet |
| `HelmInstalled=False` | `kubectl get pods -n gpu-operator` and operator logs | Chart values mismatch, image pull failure, RBAC drift |
| `DriverReady=False` | `kubectl logs -n gpu-operator -l app=nvidia-driver-daemonset` | Driver image pull, kernel module signature mismatch (Garden Linux) |
| `ValidatorPassed=False` | `kubectl get clusterpolicy -o yaml` | One of the underlying NVIDIA components is not converging |
| `WorkloadProtection=False` (deletion stuck) | `kubectl get pods -A -o json \| jq '.items[] \| select(.spec.containers[].resources.limits["nvidia.com/gpu"])'` | GPU pods are still running; the finalizer blocks uninstall until they exit |
| `Ready` stuck `Unknown` | Iterate through the input conditions; the first non-True one is the cause | See above rows |

### Operator logs

```bash
kubectl logs -n gpu-system deploy/gpu-controller-manager -c manager
```

The controller logs every reconcile decision with a structured reason. For
deep debugging, set the log verbosity higher via the `--zap-log-level=debug`
flag in the Deployment.

## Disabling the module

Remove the `gpu` entry from `Kyma.spec.modules`. Lifecycle-manager deletes the
`Gpu` CR; the operator's finalizer runs `helm uninstall`; the operator
Deployment is removed.

If GPU pods are still running on the cluster, the finalizer surfaces
`WorkloadProtection=False / ActiveGPUWorkloads` and waits - delete or drain
those pods to let uninstall proceed. The Helm uninstall itself has a 3-minute
internal timeout, after which the finalizer is removed and the
`gpu-operator` namespace is force-deleted; manual cleanup may then be
required. Do not delete the operator Deployment manually before the `Gpu` CR
is fully gone - that will orphan the NVIDIA workloads.
