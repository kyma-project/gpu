# Architecture

This page describes the operator's internal design. For user-facing concepts,
see [docs/user/](../user/).

## Component layout

```
cmd/main.go                       # Manager wiring, flag parsing
api/v1beta1/gpu_types.go          # Gpu CR types
internal/controller/
  gpu_controller.go               # Reconciler, watches, predicates
  conditions.go                   # Condition types and Ready summary
internal/detection/
  preflight.go                    # Node + OS detection
  machinetypes.go                 # GPU instance type prefixes
internal/helm/
  installer.go                    # Helm v3 SDK wrapper
  values.go                       # Spec -> Helm values mapping
internal/chart/
  embed.go                        # //go:embed for chart .tgz and values
  gpu-operator/*.tgz              # Embedded NVIDIA chart
  values/gardenlinux.yaml         # Embedded Garden Linux overrides
config/
  crd, rbac, manager, default,    # Kustomize bases
  network-policy, prometheus, ... # rendered via `make build-installer`
```

## Reconcile flow

A single controller (`GpuReconciler`) owns the complete lifecycle. The flow:

1. **Singleton guard** - reject any CR not named `gpu` with `Ready=False`.
2. **Finalizer** - add on first reconcile; the watch event re-triggers.
3. **Preflight** - call `detection.RunPreflight`.
   - `OutcomeWarn` -> `Preflight=Unknown`, requeue 30s.
   - `OutcomeError` -> `Preflight=False`, no requeue (Node watch self-heals).
   - `OutcomeProceed` -> `Preflight=True`, capture detected `OSType`, continue.
4. **Helm install/upgrade** - call `Installer.InstallOrUpgrade`.
   - Success -> `HelmInstalled=True`, record `operatorVersion`.
   - Failure -> `HelmInstalled=False`.
5. **Driver readiness** - read `nvidia-driver-daemonset` DaemonSet(s) status
   and aggregate `numberReady / desiredNumberScheduled` across DaemonSets
   (handles mixed-kernel clusters).
6. **Validator** - read `ClusterPolicy.status.state` (a string field on the
   NVIDIA CRD) via the unstructured client. Populates `ValidatorPassed`.
7. **Status apply** - compute `Ready` from the four inputs and apply the full
   status update with `RequeueAfter: 30s`.

On deletion: best-effort `HelmInstalled=Unknown`, then a workload-protection
guard - if any pod outside `gpu-operator` is Running or Pending with a
`nvidia.com/gpu` resource request, set `WorkloadProtection=False /
ActiveGPUWorkloads` and requeue without uninstalling. Once no GPU workloads
remain, run `Installer.Uninstall`, force-delete the `gpu-operator` namespace,
and remove the finalizer. If `Uninstall` exceeds the 3-minute internal
timeout, the finalizer is removed anyway and the namespace is force-deleted.

## Conditions

Six types: `Preflight`, `HelmInstalled`, `DriverReady`, `ValidatorPassed`,
`WorkloadProtection`, `Ready`. The first four are inputs to the `Ready`
summary computed by `computeReadySummary`:

- Any input `False` -> `Ready=False` (definitively broken).
- Any input `Unknown` or absent -> `Ready=Unknown` (still converging).
- All four `True` -> `Ready=True`.

`WorkloadProtection` is set independently during deletion and is **not** an
input to `Ready` - it signals that uninstall is being held back by active GPU
workloads, not that the steady-state install is unhealthy.

There is no `State` enum field on the CR. State is communicated exclusively
through conditions, aligning with KEP-1623 and current Kubernetes API
conventions.

## Watches and predicates

Three watch sources in `SetupWithManager`:

- `Gpu` (the primary resource).
- `Node` filtered by `gpuNodeChangedPredicate` - fires only on:
  - Create / delete of a GPU node.
  - Label changes that cross the GPU/non-GPU boundary.
  - OS image changes on a GPU node.
  Kubelet heartbeats (which bump `ResourceVersion` every ~10s) are filtered.
- `DaemonSet` filtered by `driverDaemonSetPredicate` - fires only for
  DaemonSets with label `app=nvidia-driver-daemonset` in `gpu-operator`.

ClusterPolicy is intentionally **not** watched: the CRD is installed by Helm
and is absent on a fresh cluster. The 30-second periodic requeue picks up
state changes there.

## Embedded artifacts

`internal/chart/embed.go` exposes:

- `chart.GPUOperatorChart()` - raw bytes of the highest-semver `.tgz` in the
  embedded directory.
- `chart.GPUOperatorChartVersion()` - the version string parsed from the
  filename.
- `chart.GardenLinuxValues()` - the embedded Garden Linux values override.

`make chart-download` and `make values-download` refresh these artifacts.
`make chart-verify` (run by `make build`) guards against missing files.

## Helm layer

`Installer` is an interface with `InstallOrUpgrade` and `Uninstall`. The
concrete type is `Client`, which wraps Helm v3 SDK `action.Configuration` with
Kubernetes secrets as the storage backend. Tests inject a `fakeInstaller`.

`BuildValues(spec, ClusterInfo)` merges the embedded Garden Linux base values
with user spec overrides for Garden Linux clusters. For Ubuntu clusters,
NVIDIA defaults are used (no base values file). `ClusterInfo.OS` is set by
preflight and is always a supported `OSType` when `BuildValues` is called.

## Concurrency

`MaxConcurrentReconciles: 1` is set explicitly. The CR is a cluster-scoped
singleton, and the status writes use server-side apply with split field
ownership; concurrency >1 provides no benefit and risks harder-to-reason-about
status races.

## Test conventions

Two styles, applied deliberately:

- **stdlib `testing`** - pure unit tests for stateless functions and predicates.
  Examples: `gpu_node_predicate_test.go`, `machinetypes_test.go`.
- **Ginkgo + Gomega** - envtest-based controller tests that need a real API
  server. `BeforeSuite` starts envtest once per suite.

`make test-rbac` runs `TestChartResourcesCoveredByRBAC`
(`internal/chart/rbac_test.go`) - CI fails if the chart produces a resource
type not covered by the RBAC markers in `gpu_controller.go`.

## RBAC

RBAC markers in `gpu_controller.go` are explicit per-resource grants - no
wildcard `*/*`. `make manifests` regenerates `config/rbac/role.yaml` from
these markers. After bumping the embedded chart, run `make test-rbac` to
catch coverage drift.
