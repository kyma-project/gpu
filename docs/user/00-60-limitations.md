# Limitations

## Supported operating systems

The module supports Garden Linux and Ubuntu on GPU nodes. Other distributions
are not supported. Mixed-OS GPU node pools are rejected at preflight.

## Supported GPU instance types

Detection of GPU nodes is by `node.kubernetes.io/instance-type` prefix:

- AWS: `g4dn`, `g6`
- GCP: `g2-`
- Azure: `Standard_NC`

Nodes outside these prefixes are not detected as GPU nodes even if they have
physical GPUs. If you need a different instance type, file an issue.

## Driver versions

For Garden Linux nodes, available driver versions are constrained by the set
of precompiled signed kernel modules published in the
`ghcr.io/gardenlinux/gardenlinux-nvidia-installer` image. Setting
`spec.driver.version` to a version that has no matching precompiled module
will fail at DaemonSet image pull.

For Ubuntu nodes, the chart's runtime driver compilation supports a wider
range, but compilation is slower and adds a failure surface.

## FIPS scope

The operator binary is built with FIPS-validated cryptographic modules
(`GOFIPS140=v1.0.0`). The NVIDIA-deployed components (driver, CUDA libraries,
DCGM, NFD) do not use FIPS-validated cryptographic modules and are out of FIPS
scope by definition - they perform no cryptographic operations.

If your environment requires that all binaries on cluster nodes be built with
FIPS toolchains regardless of whether they perform crypto, the NVIDIA
components are not compatible. Most regulated environments accept the scope
distinction; check with your compliance team.

## Singleton CR

There is exactly one `Gpu` per cluster, named `gpu`. Multi-tenant configurations
where different namespaces have different driver versions or chart values are
not supported.

## Air-gap

The chart and Garden Linux values are embedded in the operator binary, so
reconciliation does not require chart-repository access. However, the chart's
container images (driver, container-toolkit, dcgm-exporter, etc.) must be
reachable from the cluster. In air-gapped clusters, mirror the images listed
in [external-images.yaml](../../external-images.yaml) to your private
registry and configure the cluster's image-pull configuration accordingly.

## No webhook

The module ships no admission webhook. The CR is validated by a CEL rule on
the CRD; no defaulting webhook is provided.

## NetworkPolicy posture

The repo ships a `NetworkPolicy` base under `config/network-policy/` covering
the operator's own metrics endpoint, but it is not part of the default
overlay or the released `install.yaml` - apply it explicitly if you want it.
Either way, the NVIDIA workloads in `gpu-operator` are not isolated by any
shipped policy. If you need stricter network isolation for GPU workloads,
layer your own `NetworkPolicy` on the `gpu-operator` namespace.
