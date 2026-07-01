# E2E tests for the GPU operator

End-to-end tests for the `Gpu` operator. They run against an **existing** Kyma or
Gardener cluster with GPU nodes - there is no Kind path because the operator's
contract depends on real NVIDIA hardware (driver DaemonSet, ClusterPolicy,
`nvidia.com/gpu` resource).

Pattern mirrors [`kyma-project/istio/tests/e2e/`](https://github.com/kyma-project/istio/tree/main/tests/e2e):
stdlib `testing` + `github.com/stretchr/testify/require` +
`sigs.k8s.io/e2e-framework/klient/wait`. No Ginkgo, no Kind.

## Prerequisites

1. **Kubernetes cluster with at least one GPU worker node.**
   - Kyma SKR or Gardener shoot.
   - Supported instance types: AWS `g4dn.*` / `g6.*`, GCP `g2-*`, Azure `Standard_NC*`.
   - Supported OS: Garden Linux or Ubuntu (all GPU nodes must run the same OS).
2. **GPU operator pre-deployed** - `make deploy IMG=<image>` from the repo root,
   or installation via lifecycle-manager. CRDs must be installed.
3. **`KUBECONFIG`** pointing at the target cluster (or a usable `~/.kube/config`).
4. **No conflicting `Gpu` CR** must exist on the cluster - the tests create the
   singleton named `gpu` themselves.
5. **Go ≥ 1.22**. `gotestsum` is only needed for the JUnit target.

## Running

Once you have a cluster with at least one GPU node and `KUBECONFIG` pointed at
it, walk through these steps from the repo root.

### 1. Install CRDs and the controller

Pick one of two paths depending on what you're testing.

**Option A - test a released build (fastest).** Apply the published install
manifest. Substitute `latest` with a specific tag (e.g. `v0.2.0`) to pin a
release:

```bash
kubectl apply -f https://github.com/kyma-project/gpu/releases/latest/download/install.yaml
```

Do **not** apply `instance.yaml` from the release - that creates the `Gpu`
CR, which the suite needs to manage itself.

**Option B - test local changes.** Build and push your own image, then deploy
it via kustomize:

```bash
make docker-build docker-push IMG=<your-registry>/gpu-operator:dev
make install                                            # CRDs
make deploy IMG=<your-registry>/gpu-operator:dev        # controller Deployment
```

### 2. Wait for the controller to come up

```bash
kubectl get pods -n gpu-operator-system
```

The controller pod should reach `Running` before you move on. (Replace the
namespace if your `install.yaml` or kustomize overlay puts it elsewhere.)

### 3. Confirm no `Gpu` CR exists

The suite creates the singleton `gpu` CR itself, so the cluster must start
clean:

```bash
kubectl get gpu
# No resources found -> good, proceed.
# If one exists, delete it first: kubectl delete gpu gpu
```

### 4. Run the tests

Quickest sanity check first - admission-only, <30s. This proves CRDs and
kubeconfig are wired up before you commit to a 15-minute smoke run:

```bash
go test ./tests/e2e/tests/singleton/... -v
```

Then the longer ones, or everything at once:

```bash
# All three tests (30m default timeout)
make test-e2e

# Or one at a time while iterating
go test ./tests/e2e/tests/smoke/... -v -timeout 30m
go test ./tests/e2e/tests/workload_protection/... -v -timeout 30m

# With JUnit output for CI
make test-e2e-junit
```

### 5. On failure

Cluster state is written to `./logs/<timestamp>/<TestName>/` (Gpu CR,
ClusterPolicy, Nodes, plus pods and logs from the `gpu-operator` namespace).
To additionally leave the `Gpu` CR on the cluster for manual inspection:

```bash
SKIP_CLEANUP=true go test ./tests/e2e/tests/smoke/... -v -timeout 30m
```

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `KUBECONFIG` | `~/.kube/config` | Cluster to test against. |
| `GPU_OPERATOR_NAMESPACE` | `gpu-operator` | Namespace the operator's workloads live in (used by the cluster-state dumper). |
| `TEST_TIMEOUT` | `15m` | Default per-assertion poll timeout. Individual tests may override. |
| `SKIP_CLEANUP` | `false` | When `true` AND a test fails, the `Gpu` CR is left on the cluster for post-mortem. |
| `E2E_LOGS_DIR` | `./logs` | Where `DumpClusterResources` writes YAML and pod logs on failure. |

## The three tests

| Test | Wall time (~) | What it covers |
|---|---|---|
| `tests/smoke` | 14–20 min | Apply singleton `Gpu`, wait for `Ready=True` plus every input condition (`Preflight`, `HelmInstalled`, `DriverReady`, `ValidatorPassed`). Verify `status.operatorVersion` is populated and `status.driver.nodesReady >= 1`. Then delete the CR and assert the controller fully unwinds: CR gone, `gpu-operator` namespace gone, no `ClusterPolicy` left. |
| `tests/workload_protection` | 5–8 min | Install `Gpu`, deploy a pod with `nvidia.com/gpu: 1`, attempt to delete the CR. Assert the operator blocks deletion via `WorkloadProtection=False / ActiveGPUWorkloads`. Remove the pod, assert finalizer clears and the CR disappears. |
| `tests/singleton` | <30 s | Apply a `Gpu` named `not-gpu`, expect kube-apiserver to reject it via the CEL validation rule on the CRD. |

## Layout

```
tests/e2e/
├── pkg/
│   ├── config/    # env-var loading (TEST_TIMEOUT, SKIP_CLEANUP, ...)
│   ├── setup/     # DeclareCleanup, ShouldSkipCleanup, DumpClusterResources
│   ├── helpers/   # builders + apply/delete wrappers for Gpu CR and GPU pods
│   └── asserts/   # poll-based assertions on Gpu status
└── tests/
    ├── smoke/
    ├── workload_protection/
    └── singleton/
```

## Helper-writing rules

Borrowed from istio's e2e README and enforced across this suite:

- Helpers take `t *testing.T` as the first argument and call `t.Helper()`.
- Helpers return `error`; they do **not** call `require.*` themselves. Assertions
  live in `pkg/asserts/`.
- Helpers register their own cleanup via `setup.DeclareCleanup`.
- Use `t.Context()` for in-test API calls; use `setup.GetCleanupContext()`
  for deletion paths so cleanup still runs after the test context cancels.
- Use functional options (`WithName(...)`, `WithTimeout(...)`) for tunable
  behavior - no positional config structs in public signatures.

## Debugging a failure

The dump produced under `./logs/<timestamp>/<TestName>/` contains:

- `resources/` - YAML of `Gpu`, `ClusterPolicy`, `Node`, plus DaemonSets,
  Deployments, and Pods in the `gpu-operator` namespace.
- `pods/` - full container logs from every pod in the `gpu-operator` namespace.

Combine with `SKIP_CLEANUP=true` (see step 5 above) to keep the failing `Gpu`
CR alive on the cluster for live inspection.
