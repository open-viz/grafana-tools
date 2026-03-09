[![Build Status](https://github.com/open-viz/grafana-tools/workflows/CI/badge.svg)](https://github.com/open-viz/grafana-tools/actions?workflow=CI)
[![Docker Pulls](https://img.shields.io/docker/pulls/appscode/grafana-tools.svg)](https://hub.docker.com/r/appscode/grafana-tools/)
[![Twitter](https://img.shields.io/twitter/follow/OpenViz.svg?style=social&logo=twitter&label=Follow)](https://twitter.com/intent/follow?screen_name=OpenViz)

# Grafana Operator

## OpenShift 4.21 User Workload Monitoring

Use the following manifests to enable user workload monitoring and apply a minimal namespace-scoped RBAC policy.

Important notes:

- Remove any custom Prometheus instances before enabling `enableUserWorkload: true`.
- The `user-workload-monitoring-config` `ConfigMap` is typically created automatically after user workload monitoring is enabled.
- Every save to `user-workload-monitoring-config` can trigger a rollout of user workload monitoring components.

### 1) Enable User Workload Monitoring (`cluster-monitoring-config`)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-monitoring-config
  namespace: openshift-monitoring
data:
  config.yaml: |
    enableUserWorkload: true
```

Apply:

```bash
oc apply -f cluster-monitoring-config.yaml
```

Verify user workload monitoring components are running:

```bash
oc -n openshift-user-workload-monitoring get pod
```

Expected components include `prometheus-operator`, `prometheus-user-workload`, and `thanos-ruler-user-workload`.

### 2) Configure User Workload Stack (`user-workload-monitoring-config`)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: user-workload-monitoring-config
  namespace: openshift-user-workload-monitoring
data:
  config.yaml: |
    prometheus:
      retention: 15d
      resources:
        requests:
          cpu: 200m
          memory: 1Gi
        limits:
          cpu: 1
          memory: 2Gi
      volumeClaimTemplate:
        spec:
          storageClassName: <your-storage-class>
          resources:
            requests:
              storage: 50Gi
    thanosRuler:
      retention: 24h
```

Apply:

```bash
oc apply -f user-workload-monitoring-config.yaml
```

### 3) Minimal Namespace RBAC For Team Monitoring Objects

OpenShift provides built-in monitoring cluster roles. For project-scoped monitoring object management, bind `monitoring-edit` in the target project namespace.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: monitoring-edit
  namespace: my-team
subjects:
  - kind: Group
    name: my-team-developers
    apiGroup: rbac.authorization.k8s.io
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: monitoring-edit
```

Apply:

```bash
oc apply -f my-team-monitoring-rbac.yaml
```

Optional: allow a non-admin user to tune user workload monitoring config in `openshift-user-workload-monitoring`:

```bash
oc -n openshift-user-workload-monitoring adm policy add-role-to-user \
  user-workload-monitoring-config-edit <username> \
  --role-namespace openshift-user-workload-monitoring
```

### 4) Optional Alert Routing Setup For User Projects

Enable one of the following patterns:

- Platform Alertmanager integration (`cluster-monitoring-config`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-monitoring-config
  namespace: openshift-monitoring
data:
  config.yaml: |
    enableUserWorkload: true
    alertmanagerMain:
      enableUserAlertmanagerConfig: true
```

- Dedicated user Alertmanager (`user-workload-monitoring-config`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: user-workload-monitoring-config
  namespace: openshift-user-workload-monitoring
data:
  config.yaml: |
    alertmanager:
      enabled: true
      enableAlertmanagerConfig: true
```

Then bind `alert-routing-edit` in each user project namespace where users should manage `AlertmanagerConfig` resources.
