# End-to-end testing

The GPU module ships a small e2e suite under [`tests/e2e/`](../../tests/e2e/)
that exercises the operator's external contract against a real cluster. The
canonical reference for prerequisites, env vars, and run commands is
[`tests/e2e/README.md`](../../tests/e2e/README.md) - this page exists to
explain *why* the suite is shaped the way it is.

## Why no Kind

Mature SKR modules (Istio, Telemetry, API Gateway) run their e2e suites on
Kind for speed and reproducibility. The GPU operator cannot follow that
pattern: every meaningful assertion in its contract depends on real NVIDIA
hardware.

| Contract | Requires |
|---|---|
| `Preflight=True` | A GPU node detected via `node.kubernetes.io/instance-type` and a supported OS. |
| `HelmInstalled=True` | Chart installs against a node taint Kind doesn't produce. |
| `DriverReady=True` | `nvidia-driver-daemonset` reaches Ready on a node with an NVIDIA card. |
| `ValidatorPassed=True` | NVIDIA's ClusterPolicy validator probes a GPU. |
| `WorkloadProtection=False` (deletion guard) | A Pod consuming `nvidia.com/gpu` - which requires the device plugin to publish the resource. |

A virtualised Kind node has no card, no instance-type label, no device plugin,
and no driver DaemonSet. There is no useful mock layer above all of this, so
the suite targets real Kyma/Gardener clusters and assumes the operator is
already deployed there.

## What a usable test cluster looks like

- Kyma SKR shoot or Gardener shoot.
- At least one worker pool with GPU instance type. Currently supported:
  AWS `g4dn.*` / `g6.*`, GCP `g2-*`, Azure `Standard_NC*`.
- All GPU nodes on the same OS image - Garden Linux or Ubuntu. Mixed-OS GPU
  clusters are rejected by preflight (`Preflight=False`) by design.
- GPU operator pre-deployed via `make deploy IMG=<image>` or lifecycle-manager.

## CI

There is no automated CI runner today; the suite is intended to be run
manually from a developer machine pointed at an existing shoot. A follow-up
GitHub Actions / Prow workflow will provision a Gardener shoot with GPU
nodes, install the operator, run `make test-e2e-junit`, and tear down. That
workflow is intentionally out of scope until the manual flow is stable.

## Test scope

The suite covers the operator's external contract with three tests:

- **smoke** - happy path: install reaches `Ready=True`, status is populated.
- **workload_protection** - deletion is blocked while GPU workloads run, and
  clears once they're gone.
- **singleton** - the CEL admission rule on the CRD rejects non-`"gpu"` names.

Time-slicing-specific behaviour, upgrade paths, and performance/scale
scenarios are explicitly **not** covered here. Time-slicing is exercised at
the unit + envtest layer; the others are deferred until the manual run is
green.
