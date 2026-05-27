[![Build](https://github.com/kyma-project/gpu/actions/workflows/image-build-main.yaml/badge.svg)](https://github.com/kyma-project/gpu/actions/workflows/image-build-main.yaml)

# GPU

## Overview

The GPU module manages the [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator) lifecycle on [SAP BTP, Kyma runtime](https://help.sap.com/docs/btp/sap-business-technology-platform/kyma-environment) clusters. It handles installation, upgrades, and health monitoring through a single `Gpu` custom resource.

The operator embeds the NVIDIA GPU Operator Helm chart and Garden Linux driver values directly in the binary - no network access is needed during reconciliation. Pre-compiled drivers are applied automatically for Garden Linux nodes, skipping runtime kernel module compilation.

## Prerequisites

- A Kyma cluster with GPU machine types (AWS g4dn/g6, GCP g2, Azure Standard_NC)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)

## Installation

1. Install the GPU operator (CRD, RBAC, and controller):

   ```bash
   kubectl apply -f https://github.com/kyma-project/gpu/releases/latest/download/install.yaml
   ```

2. Enable GPU support on your cluster by creating a `Gpu` resource:

   ```bash
   kubectl apply -f https://github.com/kyma-project/gpu/releases/latest/download/instance.yaml
   ```

3. Verify the installation:

   ```bash
   kubectl get gpu
   ```

   After successful installation:

   ```
   NAME   READY   REASON   DRIVER VERSION   NODES READY   AGE
   gpu    True    Ready    590              1             5m
   ```

To install a specific version, replace `latest` with the version number, for example `0.1.1`:

```bash
kubectl apply -f https://github.com/kyma-project/gpu/releases/download/0.1.1/install.yaml
kubectl apply -f https://github.com/kyma-project/gpu/releases/download/0.1.1/instance.yaml
```

## Usage

The `Gpu` resource is a cluster-scoped singleton. Once applied, the operator installs the NVIDIA GPU Operator and monitors its status. You can optionally pin a specific driver version:

```yaml
apiVersion: gpu.kyma-project.io/v1beta1
kind: Gpu
metadata:
  name: gpu
spec:
  driver:
    version: "590.48.01"  # optional, omit to use the default
```

Status conditions reflect the current state:

| Condition | Description |
|---|---|
| `Preflight` | Garden Linux GPU nodes detected and validated |
| `HelmInstalled` | NVIDIA GPU Operator chart installed or upgraded |
| `DriverReady` | NVIDIA driver DaemonSet has nodes ready |
| `ValidatorPassed` | NVIDIA operator validator completed successfully |
| `Ready` | Summary of all conditions |

## Development

```bash
# Download the embedded NVIDIA chart and Garden Linux values (required before build)
make chart-download
make values-download

# To replace an existing chart with a newer version, use chart-refresh instead of chart-download
make chart-refresh

# Build and test
make build
make test

# Run the controller locally against your current cluster
make run
```

### GoLand

To run and debug the controller against a real cluster from GoLand:

1. Open **Run > Edit Configurations** and add a new **Go Build** configuration
2. Set kind to **Package** and package path to `github.com/kyma-project/gpu/cmd`
3. Add environment variable `KUBECONFIG` pointing to your cluster kubeconfig
4. Run or debug - the controller connects to the cluster and starts reconciling immediately
5. Apply the `Gpu` CR to trigger reconciliation: `kubectl apply -f config/samples/gpu_v1beta1_gpu.yaml`

Make sure `make chart-download && make values-download` have been run first so the embedded artifacts exist.

## Embedded NVIDIA GPU Operator Chart

This operator embeds the [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator) Helm chart in the binary via Go's `//go:embed`. No network access needed during reconciliation.

For Garden Linux clusters, pre-compiled driver values are applied automatically (no runtime kernel module compilation). See [gardenlinux-nvidia-installer](https://github.com/gardenlinux/gardenlinux-nvidia-installer) for details.

### Paths

- Chart: `internal/chart/gpu-operator/gpu-operator-<VERSION>.tgz`
- Garden Linux values: `internal/chart/values/gardenlinux.yaml`
- Go embed package: `internal/chart/embed.go`
- Download scripts: `hack/download-chart.sh`, `hack/download-values.sh`

### Make targets

| Target | Description |
|--------|-------------|
| `make chart-download` | Add a chart version (keeps existing) |
| `make chart-refresh` | Remove all charts, download latest |
| `make values-download` | Download latest Garden Linux values |
| `make chart-verify` | Verify files exist (runs before build) |

Pin versions: `make chart-download NVIDIA_GPU_OPERATOR_VERSION=v26.3.1` or `make values-download GARDENLINUX_NVIDIA_INSTALLER_VERSION=1.7.1`

### Build requirement

Both the chart `.tgz` and `gardenlinux.yaml` must exist before `go build`. Run download targets first or place files manually, e.g.:

- Chart: https://helm.ngc.nvidia.com/nvidia/charts/gpu-operator-v26.3.1.tgz
- Values: https://raw.githubusercontent.com/gardenlinux/gardenlinux-nvidia-installer/refs/tags/1.7.1/helm/gpu-operator-values.yaml

## Contributing
<!--- mandatory section - do not change this! --->

See the [Contributing Rules](CONTRIBUTING.md).

## Code of Conduct
<!--- mandatory section - do not change this! --->

See the [Code of Conduct](CODE_OF_CONDUCT.md) document.

## Licensing
<!--- mandatory section - do not change this! --->

See the [license](./LICENSE) file.
