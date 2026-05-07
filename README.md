> **NOTE:** This is a general template that you can use for a project README.md. Except for the mandatory sections, use only those sections that suit your use case but keep the proposed section order.
>
> Mandatory sections: 
> - `Overview`
> - `Prerequisites`, if there are any requirements regarding hard- or software
> - `Installation`
> - `Contributing` - do not change this!
> - `Code of Conduct` - do not change this!
> - `Licensing` - do not change this!

# {Project Title}
<!--- mandatory --->
> Modify the title and insert the name of your project. Use Heading 1 (H1).

## Overview
<!--- mandatory section --->

> Provide a description of the project's functionality.
>
> If it is an example README.md, describe what the example illustrates.

## Prerequisites

> List the requirements to run the project or example.

## Installation

> Explain the steps to install your project. If there are multiple installation options, mention the recommended one and include others in a separate document. Create an ordered list for each installation task.
>
> If it is an example README.md, describe how to build, run locally, and deploy the example. Format the example as code blocks and specify the language, highlighting where possible. Explain how you can validate that the example ran successfully. For example, define the expected output or commands to run which check a successful deployment.
>
> Add subsections (H3) for better readability.

## Usage

> Explain how to use the project. You can create multiple subsections (H3). Include the instructions or provide links to the related documentation.

## Development

> Add instructions on how to develop the project or example. It must be clear what to do and, for example, how to trigger the tests so that other contributors know how to make their pull requests acceptable. Include the instructions or provide links to related documentation.

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
