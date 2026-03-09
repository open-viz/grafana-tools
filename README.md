[![Build Status](https://github.com/open-viz/grafana-tools/workflows/CI/badge.svg)](https://github.com/open-viz/grafana-tools/actions?workflow=CI)
[![Docker Pulls](https://img.shields.io/docker/pulls/appscode/grafana-tools.svg)](https://hub.docker.com/r/appscode/grafana-tools/)
[![Twitter](https://img.shields.io/twitter/follow/OpenViz.svg?style=social&logo=twitter&label=Follow)](https://twitter.com/intent/follow?screen_name=OpenViz)

# Grafana Operator

## OpenShift 4.21 User Workload Monitoring

Use the following manifests to enable user workload monitoring and apply a minimal namespace-scoped RBAC policy.

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
          storageClassName: gp3-csi
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

This policy allows a team group to manage `ServiceMonitor`, `PodMonitor`, and `PrometheusRule` only in its own namespace.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: user-workload-monitoring-editor
  namespace: my-team
rules:
  - apiGroups: ["monitoring.coreos.com"]
    resources: ["servicemonitors", "podmonitors", "prometheusrules"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: user-workload-monitoring-editor
  namespace: my-team
subjects:
  - kind: Group
    name: my-team-developers
    apiGroup: rbac.authorization.k8s.io
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: user-workload-monitoring-editor
```

Apply:

```bash
oc apply -f my-team-monitoring-rbac.yaml
```
