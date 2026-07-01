# Contributor Documentation

Engineering documentation for the GPU module operator.

## Topics

- [Architecture](./architecture.md) - controller design, conditions, embedded chart
- [Local setup](./local-setup.md) - running the operator against a real cluster
- [E2E testing](./e2e-testing.md) - shape and prerequisites of the e2e suite
- [Releasing](./releasing.md) - release workflow and module submission

## Contributing

See the top-level [CONTRIBUTING.md](../../CONTRIBUTING.md) for the contribution
process. Pull requests touching `internal/chart/` (chart bumps) require
coordinated updates to `external-images.yaml` and `sec-scanners-config.yaml`
in the same PR - see [releasing.md](./releasing.md#nvidia-chart-bumps).
