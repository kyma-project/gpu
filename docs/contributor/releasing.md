# Releasing

How to cut a release and submit the new version to the Kyma module catalog.

## Versioning policy

- **Patch** (`0.1.0` -> `0.1.1`) - bug fixes only. No API changes, no chart
  bump.
- **Minor** (`0.1.0` -> `0.2.0`) - new features, NVIDIA chart bump, optional
  new CR fields. No breaking changes.
- **Major** (`0.x` -> `1.0.0`) - breaking API changes; reserved for graduation
  to GA.

## Cutting a release

### 1. Make sure `main` is green

Wait for the latest commit on `main` to pass CI: lint, test, image build,
RBAC coverage. The release workflow runs validate -> build-image -> test ->
tag -> publish; if any of those would fail, fix it on `main` first.

### 2. (Minor releases only) cut a release branch

```bash
git checkout -b release-0.2 main
git push -u origin release-0.2
```

Patch releases stay on the existing `release-x.y` branch.

### 3. Run the Release workflow

`Actions -> Release -> Run workflow`:

- `version`: bare semver, e.g. `0.2.0`
- `ref`: the branch or commit to release from (typically `release-0.2`)

The workflow:

1. Validates version format and that the tag does not already exist.
2. Builds and pushes the image to
   `europe-docker.pkg.dev/kyma-project/prod/gpu:<version>`.
3. Runs `make build` and `make test`.
4. Creates the annotated git tag.
5. Renders `dist/install.yaml` and `dist/instance.yaml` via
   `make build-installer`.
6. Generates release notes from the commit log since the previous tag.
7. Publishes the GitHub Release with both YAML files attached.

### 4. Submit to the Kyma module catalog

Submission to the Kyma module catalog (which makes the new version visible
to lifecycle-manager) is **not** automated from this repo and is not covered
by the release workflow. Coordinate the submission with the Kyma module
owners after the GitHub Release is live and the operator image is present at
`europe-docker.pkg.dev/kyma-project/prod/gpu:<version>`.

## NVIDIA chart bumps

A chart bump is a coordinated four-file change. All four must move in a
**single PR**:

1. **Refresh the chart**:
   ```bash
   make chart-refresh
   ```
2. **Extract the new image tags**:
   ```bash
   helm show values internal/chart/gpu-operator/*.tgz | grep -E '(repository|tag|version):'
   ```
3. **Update `external-images.yaml`** with the new tags. The
   `kyma-project/test-infra` image-syncer workflow mirrors these to
   `europe-docker.pkg.dev/kyma-project/prod/external/...`; the mirror path is
   derived by the syncer, not declared here.
4. **Update `sec-scanners-config.yaml` only if `rc-tag` changed.** This file
   tracks the operator's own image (under `bdba:`); it does not list
   third-party NVIDIA images.
5. **Validate**:
   ```bash
   make build         # verifies chart embed
   make test          # unit + envtest
   make test-rbac     # chart RBAC coverage
   make build-installer  # smoke-test rendered manifest
   ```
6. **Manual E2E** on a Gardener cluster with GPU nodes.
7. **Open the PR.** Once `external-images.yaml` lands on `main`, the
   `kyma-project/test-infra` image-syncer mirrors the new tags. CI scans the
   operator image listed in `sec-scanners-config.yaml`; third-party NVIDIA
   images are not scanned by this module's pipeline.

## Beta -> GA promotion

The criteria for promoting the module from `fast` to `regular` are not
defined in this repo. They are owned by the Kyma module governance process;
coordinate with the module owners when the team considers the module ready.
