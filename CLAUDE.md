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
Cluster-scoped singleton resource. Spec allows an optional override for driver version (`spec.driver.version`). Status tracks `operatorVersion`, `driver.version`, `driver.nodesReady`, and five conditions.

There is no `State` enum field on the CRD. State is communicated exclusively through the conditions system described below.

### Two Controllers (`internal/controller/`)

**`GpuReconciler`** (`gpu_controller.go`) — owns installation:
1. Add finalizer on first reconcile
2. Run `detection.RunPreflight` — OutcomeWarn → set `Preflight=Unknown`, requeue 30s; OutcomeError → set `Preflight=False`, return with no requeue (self-heals via Node watch); OutcomeProceed → set `Preflight=True`, continue
3. Load embedded chart bytes via `chart.GPUOperatorChart()` + build Helm values via `helm.BuildValues`
4. Call `Installer.InstallOrUpgrade` — on success sets `HelmInstalled=True` and records `operatorVersion`; on failure sets `HelmInstalled=False`
5. On deletion: best-effort `HelmInstalled=Unknown` status update, then `Installer.Uninstall`, then remove finalizer
6. Watches `Node` objects via `gpuNodeChangedPredicate` — fires on GPU node create/delete and on OS image or instance-type label changes, suppressing kubelet heartbeats. Enqueues all `Gpu` CRs so preflight errors self-heal when nodes are replaced.

**`GpuStatusReconciler`** (`gpu_status_reconciler.go`) — owns status monitoring:
- Guard: skips reconcile if `HelmInstalled` condition is not True (absent, False, or Unknown — Helm hasn't succeeded yet)
- Reads `nvidia-driver-daemonset` DaemonSet status counters → sets `DriverReady` condition on the Gpu CR
- Reads `ClusterPolicy.status.state` (a plain string field on the NVIDIA CRD) via unstructured client → sets `ValidatorPassed` condition on the Gpu CR
- Recomputes `Ready` summary from all four managed conditions after every sync
- Always returns `RequeueAfter: 30s` as a polling safety net

### Condition System (`internal/controller/conditions.go`)
Five stable condition types: `Preflight`, `HelmInstalled`, `DriverReady`, `ValidatorPassed`, `Ready`.

The first four are **inputs** — each controller writes the conditions it owns:
- `GpuReconciler` writes `Preflight` and `HelmInstalled`
- `GpuStatusReconciler` writes `DriverReady` and `ValidatorPassed`

`Ready` is a **computed summary** — derived from the four inputs by `computeReadySummary`:
- Any input is `False` → `Ready=False` (definitively broken)
- Any input is `Unknown` or absent → `Ready=Unknown` (still converging)
- All four are `True` → `Ready=True`

All conditions use the tri-state (`True` / `False` / `Unknown`). `False` means definitively broken and requires user action. `Unknown` means still converging — the controller is waiting. `conditionMatches` compares status+reason+message to skip no-op status patches.

### Helm Layer (`internal/helm/`)
- `Installer` is an interface (`interface.go`) with `InstallOrUpgrade` and `Uninstall`. The concrete type is `Client` (`installer.go`), which wraps Helm v3 SDK `action.Configuration` with Kubernetes secrets as the storage backend. Tests inject a `fakeInstaller`.
- `BuildValues(spec, ClusterInfo)` merges the embedded Garden Linux base values with user spec overrides. `ClusterInfo.GardenLinux` is always `true` in production (preflight guarantees it).

### Detection (`internal/detection/`)
- `IsGPUNode(labels)` checks `node.kubernetes.io/instance-type` (exported as `detection.InstanceTypeLabel`) against known GPU prefixes (AWS g4dn/g6, GCP g2-, Azure Standard_NC).
- `RunPreflight` returns Proceed/Warn/Error: no GPU nodes → Warn; any non-Garden-Linux GPU node → Error.

### Embedded Artifacts
`internal/chart/gpu-operator/*.tgz` and `internal/chart/values/gardenlinux.yaml` are embedded via `//go:embed`. They must exist before building; `make chart-download` and `make values-download` fetch them. The `build` target runs `chart-verify` to guard against missing files.

`chart.GPUOperatorChart()` returns the raw bytes of the highest semver `.tgz` in the embedded directory (parsed via `github.com/Masterminds/semver/v3`). `chart.GPUOperatorChartVersion()` returns the version string of the same file by trimming the `gpu-operator-` prefix and `.tgz` suffix from the filename.

### Testing Conventions
Two styles are used deliberately:
- **stdlib `testing`** — for pure unit tests (stateless functions, predicates). See `gpu_node_predicate_test.go`, `machinetypes_test.go`.
- **Ginkgo + Gomega** — for envtest-based controller tests that need a real API server. `BeforeSuite` starts envtest once per suite; `BeforeEach`/`AfterEach` manage per-test state. See `suite_test.go`, `gpu_controller_test.go`, `gpu_status_reconciler_test.go`.

### GoLand Debugging
Run configuration: **Go Build**, kind = **Package**, package path = `github.com/kyma-project/gpu/cmd`. Set `KUBECONFIG` env var to your cluster kubeconfig. The controller will connect to the remote cluster and begin reconciling immediately.

## Key Constraints
- Only Garden Linux nodes are supported for GPU workloads (v1 scope). Non-Garden-Linux GPU nodes → `Preflight=False`, no automatic requeue — but the Node watch self-heals when the node is replaced or its OS image changes.
- The embedded chart must be `.tgz` files in `internal/chart/gpu-operator/`. If multiple versions exist, `chart.GPUOperatorChart()` picks the highest semver.
- RBAC markers in `gpu_controller.go` use a catch-all `"*"/"*"` rule because Helm applies arbitrary CRDs. `make manifests` regenerates `config/rbac/role.yaml` from these markers.
