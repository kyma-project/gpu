# Installation

The recommended installation path is through the Kyma module system. A direct
manifest install is also documented for development and debugging.

## Through the Kyma module system

Once the GPU module is registered in `module-manifests`, enable it on a Kyma
cluster by editing the `Kyma` CR in the Kyma Control Plane:

```yaml
apiVersion: operator.kyma-project.io/v1beta2
kind: Kyma
metadata:
  name: <skr-name>
  namespace: kcp-system
spec:
  channel: fast
  modules:
    - name: gpu
```

Lifecycle-manager will:

1. Pull the module's `ModuleTemplate` for the requested channel.
2. Deploy the operator (CRD, RBAC, controller Deployment) into `gpu-system` on
   the SKR cluster.
3. Apply the default `Gpu` CR.
4. Watch the operator's Deployment and the `Gpu.status.conditions[Ready]`
   condition, surfacing module health back through the `Kyma` CR.

## Direct install (development / disconnected clusters)

If you are not using lifecycle-manager - e.g. running locally against a
single cluster or in an air-gapped environment - install the operator and
default CR directly:

```bash
# 1. Install operator (CRD, RBAC, controller Deployment).
kubectl apply -f https://github.com/kyma-project/gpu/releases/latest/download/install.yaml

# 2. Create the Gpu resource to enable GPU support.
kubectl apply -f https://github.com/kyma-project/gpu/releases/latest/download/instance.yaml
```

## Verifying installation

```bash
kubectl get gpu
```

A healthy install reports:

```
NAME   READY   REASON   DRIVER VERSION   NODES READY   AGE
gpu    True    Ready    580.126.20       3             5m
```

If `READY` is not `True`, inspect the conditions:

```bash
kubectl describe gpu gpu
```

Each input condition (`Preflight`, `HelmInstalled`, `DriverReady`,
`ValidatorPassed`) carries a reason and message that points at the
underlying cause. See [00-50-operations.md](./00-50-operations.md) for triage
guidance.

## Uninstalling

To uninstall through lifecycle-manager, remove the `gpu` entry from
`Kyma.spec.modules`. Lifecycle-manager will delete the `Gpu` CR and the
operator Deployment; the operator's finalizer will run `helm uninstall` to
remove the NVIDIA workloads from the SKR.

To uninstall directly, delete the `Gpu` CR first, wait for the finalizer to
complete, then delete the operator manifests:

```bash
kubectl delete gpu gpu
kubectl delete -f https://github.com/kyma-project/gpu/releases/latest/download/install.yaml
```
