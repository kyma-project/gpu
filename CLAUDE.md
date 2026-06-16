# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build (requires embedded chart; run chart-download first if missing)
make build

# Run tests (unit only, uses envtest)
make test

# Run a single test package
KUBEBUILDER_ASSETS="$(bin/setup-envtest use --bin-dir bin -p path)" go test ./internal/helm/... -v -run TestLoadChart

# Verify chart RBAC coverage (run after bumping the embedded chart)
make test-rbac

# Lint
make lint

# Generate CRD manifests and DeepCopy methods after API changes
make manifests generate

# Run controller locally against current kubeconfig cluster
make run

# Download/refresh embedded NVIDIA GPU Operator chart
make chart-download      # add latest chart
make chart-refresh       # replace all charts with fresh download
make values-download     # refresh Garden Linux values

# Verify embedded chart and values exist (required before build)
make chart-verify

# Install CRDs into cluster
make install

# Deploy controller into cluster
make deploy IMG=<image>
```

## Architecture

This is a Kubernetes operator (Kubebuilder v4) that manages the NVIDIA GPU Operator lifecycle on Kyma clusters.

### CRD: `Gpu` (`api/v1beta1/`)
Cluster-scoped singleton resource. Spec allows an optional override for driver version (`spec.driver.version`). Status tracks `operatorVersion`, `driver.version`, `driver.nodesReady`, and a set of conditions.

There is no `State` enum field on the CRD. State is communicated exclusively through the conditions system described below.

### Single Controller (`internal/controller/`)

**`GpuReconciler`** (`gpu_controller.go`) — owns the full lifecycle: installation, status monitoring, and deletion.

Reconcile flow (happy path):
1. Singleton guard: rejects any CR not named `"gpu"` with `Ready=False`
2. Add finalizer on first reconcile (return; watch event re-triggers)
3. Run `detection.RunPreflight` — OutcomeWarn → set `Preflight=Unknown`, requeue 30s; OutcomeError → set `Preflight=False`, return with no requeue (self-heals via Node watch); OutcomeProceed → set `Preflight=True`, capture detected `OSType`, continue
4. Load embedded chart bytes via `chart.GPUOperatorChart()` + build Helm values via `helm.BuildValues`
5. Call `Installer.InstallOrUpgrade` — on success sets `HelmInstalled=True` and records `operatorVersion`; on failure sets `HelmInstalled=False`
6. Read `nvidia-driver-daemonset` DaemonSet(s) status counters → set `DriverReady` condition (aggregates across multiple DaemonSets for mixed-kernel clusters)
7. Read `ClusterPolicy.status.state` (a plain string field on the NVIDIA CRD) via unstructured client → set `ValidatorPassed` condition
8. Compute `Ready` summary and apply all status fields; return `RequeueAfter: 30s`

On deletion: best-effort `HelmInstalled=Unknown` status update, then `Installer.Uninstall`, then remove finalizer.

Watches:
- `Node` objects via `gpuNodeChangedPredicate` — fires on GPU node create/delete, when a node transitions into or out of GPU membership (instance-type label changes that cross the GPU/non-GPU boundary), or when the OS image changes on a GPU node. Kubelet heartbeats are suppressed. Enqueues all `Gpu` CRs so preflight errors self-heal when nodes are replaced.
- `DaemonSet` objects via `driverDaemonSetPredicate` — fires only for DaemonSets with label `app=nvidia-driver-daemonset` in the `gpu-operator` namespace, so driver rollout state transitions trigger reconciliation.

### Condition System (`internal/controller/conditions.go`)
Six condition types: `Preflight`, `HelmInstalled`, `DriverReady`, `ValidatorPassed`, `WorkloadProtection`, `Ready`.

The first four are **inputs** written by `GpuReconciler` during the install/upgrade reconcile. `Ready` is a **computed summary** derived by `computeReadySummary`:
- Any input is `False` → `Ready=False` (definitively broken)
- Any input is `Unknown` or absent → `Ready=Unknown` (still converging)
- All four are `True` → `Ready=True`

`WorkloadProtection` is set independently during deletion (`reconcileDelete`) when GPU pods are still active and is not an input to `Ready`.

All conditions use the tri-state (`True` / `False` / `Unknown`). `False` means definitively broken and requires user action. `Unknown` means still converging.

### Helm Layer (`internal/helm/`)
- `Installer` is an interface (`interface.go`) with `InstallOrUpgrade` and `Uninstall`. The concrete type is `Client` (`installer.go`), which wraps Helm v3 SDK `action.Configuration` with Kubernetes secrets as the storage backend. Tests inject a `fakeInstaller`.
- `BuildValues(spec, ClusterInfo)` merges the embedded Garden Linux base values with user spec overrides for Garden Linux clusters. For Ubuntu clusters, NVIDIA defaults are used (no base values file). `ClusterInfo.OS` is set by preflight and is always a supported `OSType` when `BuildValues` is called.

### Detection (`internal/detection/`)
- `IsGPUNode(labels)` checks `node.kubernetes.io/instance-type` (exported as `detection.InstanceTypeLabel`) against known GPU prefixes (AWS g4dn/g6, GCP g2-, Azure Standard_NC).
- `RunPreflight` returns Proceed/Warn/Error: no GPU nodes → Warn; any GPU node with unsupported OS → Error; mixed OS types across GPU nodes → Error; all GPU nodes on same supported OS → Proceed with detected `OSType` (`OSTypeGardenLinux` or `OSTypeUbuntu`). OS is detected from `node.Status.NodeInfo.OSImage` (case-insensitive substring match).

### Embedded Artifacts
`internal/chart/gpu-operator/*.tgz` and `internal/chart/values/gardenlinux.yaml` are embedded via `//go:embed`. They must exist before building; `make chart-download` and `make values-download` fetch them. The `build` target runs `chart-verify` to guard against missing files.

`chart.GPUOperatorChart()` returns the raw bytes of the highest semver `.tgz` in the embedded directory. `chart.GPUOperatorChartVersion()` returns the version string. `chart.GardenLinuxValues()` returns the embedded Garden Linux values override.

### Testing Conventions
Two styles are used deliberately:
- **stdlib `testing`** — for pure unit tests (stateless functions, predicates). See `gpu_node_predicate_test.go`, `machinetypes_test.go`.
- **Ginkgo + Gomega** — for envtest-based controller tests that need a real API server. `BeforeSuite` starts envtest once per suite; `BeforeEach`/`AfterEach` manage per-test state. See `suite_test.go`, `gpu_controller_test.go`.

`make test-rbac` runs `TestChartResourcesCoveredByRBAC` (in `internal/chart/rbac_test.go`) — CI fails if the chart produces a resource type not covered by the RBAC markers in `gpu_controller.go`.

### GoLand Debugging
Run configuration: **Go Build**, kind = **Package**, package path = `github.com/kyma-project/gpu/cmd`. Set `KUBECONFIG` env var to your cluster kubeconfig. The controller will connect to the remote cluster and begin reconciling immediately.

## Key Constraints
- Garden Linux and Ubuntu nodes are supported. All GPU nodes must run the same OS — mixed clusters are rejected at preflight (`Preflight=False`). Non-Garden-Linux, non-Ubuntu GPU nodes → `Preflight=False`, no automatic requeue — but the Node watch self-heals when the node is replaced or its OS image changes.
- The embedded chart must be `.tgz` files in `internal/chart/gpu-operator/`. If multiple versions exist, `chart.GPUOperatorChart()` picks the highest semver.
- RBAC markers in `gpu_controller.go` are explicit per-resource grants (no wildcard `*/*`). `make manifests` regenerates `config/rbac/role.yaml` from these markers. `make test-rbac` verifies coverage after chart upgrades.
- Singleton enforcement is dual-layered: a CEL validation rule on the CRD rejects non-`"gpu"` names at admission, and the controller also rejects them at reconcile time as defense-in-depth.
