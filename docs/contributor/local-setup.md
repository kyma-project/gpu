# Local Setup

This page covers running the operator from your local checkout against a
real cluster. For unit and envtest workflows, see the top-level
[CLAUDE.md](../../CLAUDE.md).

## Prerequisites

- Go 1.26.3 (matches `go.mod` and the Dockerfile builder stage).
- A Kyma or Gardener cluster with a GPU node pool. K3D cannot emulate GPU
  scheduling - local development against a real GPU cluster is required for
  meaningful work.
- `kubectl` configured against the target cluster.
- `make`.

## First-time bootstrap

The chart and Garden Linux values must be present before building:

```bash
make chart-download    # fetches the latest NVIDIA chart .tgz
make values-download   # fetches Garden Linux values overrides
make build             # builds the manager binary
make test              # runs unit + envtest suite
```

If `make build` fails complaining about missing chart artifacts, run
`chart-download` and `values-download` again.

## Running the controller against a cluster

The fastest dev loop is `make run`, which executes the controller from your
host pointed at the current `kubectl` context:

```bash
make run
```

This does not require an image build or push. The controller will connect to
the cluster, install CRDs, and begin reconciling. Logs go to stdout.

Apply the sample CR in another shell:

```bash
kubectl apply -f config/samples/gpu_v1beta1_gpu.yaml
kubectl get gpu -w
```

## Running an image build

To exercise the in-cluster path:

```bash
# Build and push to your own registry.
make docker-build docker-push IMG=<your-registry>/gpu:dev

# Deploy to the cluster pointed to by your kubeconfig.
make deploy IMG=<your-registry>/gpu:dev
```

`make deploy` uses kustomize to apply `config/default`, which includes the
CRD, RBAC, and manager Deployment. The `ServiceMonitor` and `NetworkPolicy`
bases under `config/prometheus/` and `config/network-policy/` are
**not** included in the default overlay - uncomment them in
`config/default/kustomization.yaml` (or apply those bases separately) if you
want them.

## Tearing down

```bash
make undeploy
make uninstall   # removes CRDs
```

## Debugging in IDE

Run configuration: **Go Build**, kind = **Package**, package path
`github.com/kyma-project/gpu/cmd`. Set `KUBECONFIG` to your cluster
kubeconfig as an env var. The controller connects to the remote cluster on
start.

## Refreshing the embedded chart

When the upstream NVIDIA chart releases a new version:

```bash
make chart-refresh   # replaces all embedded chart .tgz files with a fresh download
```

After bumping the chart, before opening a PR:

1. Update `external-images.yaml` with the new image tags.
2. Update `sec-scanners-config.yaml` atomically.
3. Run `make build && make test && make test-rbac`.

See [releasing.md](./releasing.md#nvidia-chart-bumps) for the full
checklist.
