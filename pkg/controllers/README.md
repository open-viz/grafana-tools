# Controllers and Reconciler Behavior

This directory contains reconcilers that connect cluster monitoring resources with backend registration and Grafana APIs.

## Runtime Wiring

Controller registration is split by process:

- `pkg/apiserver/apiserver.go`
  - Always registers the CRD bootstrap reconciler from `kmodules.xyz/client-go/apiextensions`.
  - Registers `ranchertoken.TokenRefresher` only when `ExtraConfig.RancherAuthSecret` is set.
  - Registers Prometheus, Alertmanager, and ServiceMonitor controllers through `apiextensions.RegisterSetup(...)`, so they are activated after the corresponding CRD/API kind is available.
- `pkg/cmds/server/grafana.go`
  - Registers `openviz.GrafanaDashboardReconciler` and `openviz.GrafanaDatasourceReconciler`.

## Common Reconcile Patterns

Across controllers, these patterns are heavily used:

- Detector readiness gating (`d.Ready()`) before doing work.
- Finalizer-based external cleanup.
- `CreateOrPatch` reconciliation for owned resources.
- Marker annotations to avoid repeated backend registration.
- Additional watches with predicates to requeue on dependency changes.

## Reconciler Summary

| Reconciler | Primary resource | Watches | Key outputs |
| --- | --- | --- | --- |
| `prometheus.PrometheusReconciler` | `monitoringv1.Prometheus` | Prometheus + selected ConfigMap + optional Rancher auth Secret | Finalizer, RBAC, presets, backend registration, default Prometheus/Grafana AppBindings |
| `alertmanager.AlertmanagerReconciler` | `monitoringv1.Alertmanager` | Alertmanager + inbox APIService | Alertmanager selector patch + `AlertmanagerConfig/inbox-agent` |
| `servicemonitor.AutoReconciler` | `monitoringv1.ServiceMonitor` (`.../prometheus=auto`) | Matching ServiceMonitors + Prometheus | Label patching to bind to chosen Prometheus |
| `servicemonitor.FederationReconciler` | `monitoringv1.ServiceMonitor` (`.../prometheus=federated`) | Matching ServiceMonitors + Services + Prometheus | Fan-out ServiceMonitor copies, plus copied Service/Endpoints/Secret objects |
| `namespace.ClientOrgReconciler` | `corev1.Namespace` (client-org labels) | Matching Namespaces + trickster ServiceAccount | Monitoring namespace/bootstrap RBAC, backend registration, client-org Grafana AppBinding, dashboard copy/transform |
| `ranchertoken.TokenRefresher` | `corev1.Secret` | Configured Rancher auth Secret | Token renewal scheduling and refresh |
| `openviz.GrafanaDashboardReconciler` | `openvizapi.GrafanaDashboard` | GrafanaDashboard + AppBinding | External dashboard CRUD + status and events |
| `openviz.GrafanaDatasourceReconciler` | `openvizapi.GrafanaDatasource` | GrafanaDatasource | External datasource CRUD + status and events |

## Detailed Behavior

### prometheus.PrometheusReconciler

Primary behavior:

1. Fetches `Prometheus`, waits for detector readiness, and computes default/project mode.
2. Adds finalizer `monitoring.appscode.com/prometheus` for active objects.
3. On deletion:
   - Deletes related chart preset (`ClusterChartPreset` or project `ChartPreset`).
   - Unregisters backend context when backend client is configured.
   - Removes the finalizer.
4. Reconciles in-cluster access setup:
   - Chooses Rancher token auth or service-account token auth.
   - Ensures cluster role `appscode:trickster:proxy`.
   - Ensures rolebinding `appscode:trickster:proxy` in Prometheus namespace.
5. Registers Prometheus endpoint to backend when state markers changed.
6. For default Prometheus, creates/updates:
   - AppBinding `default-prometheus`.
   - AppBinding `default-grafana`.
   - Secret `default-grafana-auth`.

Watch details:

- `For(Prometheus)`.
- Watches `ConfigMap` `kube-public/ace-info` and enqueues all Prometheus objects.
- Optionally watches configured Rancher auth secret and enqueues all Prometheus objects.

### alertmanager.AlertmanagerReconciler

Primary behavior:

1. Fetches `Alertmanager`, waits for detector readiness.
2. Applies OpenShift-specific defaulting: only reconciles user-workload Alertmanager.
3. Skips deleted objects.
4. Locates inbox APIService by group `inbox.monitoring.appscode.com`.
5. Patches Alertmanager to enforce:
   - `spec.alertmanagerConfigSelector.matchLabels[app.kubernetes.io/name]=inbox-agent`.
   - matcher strategy type `None`.
6. Ensures `AlertmanagerConfig` named `inbox-agent` with webhook receiver URL targeting inbox service.

Watch details:

- `For(Alertmanager)`.
- Watches matching `APIService` objects and requeues all Alertmanagers.

### servicemonitor.AutoReconciler

Scope: ServiceMonitors with `monitoring.appscode.com/prometheus=auto`.

Primary behavior:

1. Exits unless label value is `auto`.
2. Lists Prometheus objects and exits if none.
3. If any Prometheus already selects the ServiceMonitor (namespace selector + label selector), does nothing.
4. Otherwise selects a target Prometheus:
   - non-Rancher: first sorted Prometheus.
   - Rancher: chooses system/project Prometheus using namespace project metadata.
5. Patches ServiceMonitor labels to satisfy target Prometheus selector.

Watch details:

- `Named("servicemonitor-auto")`.
- `For(ServiceMonitor)` with predicate `.../prometheus=auto`.
- Watches `Prometheus` and requeues all matching auto ServiceMonitors.

### servicemonitor.FederationReconciler

Scope: ServiceMonitors with `monitoring.appscode.com/prometheus=federated`.

Primary behavior:

1. Exits unless label value is `federated`.
2. Lists Prometheus objects and waits for detector readiness.
3. Non-federated mode: only patches labels for first Prometheus.
4. Federated mode:
   - Updates labels on default Prometheus path.
   - For each non-default Prometheus:
     - Creates/patches copied ServiceMonitor in Prometheus namespace.
     - Injects namespace keep relabel rule at endpoint metricRelabel config head.
     - Copies selected Service objects.
     - Copies Endpoints and rewrites targets to copied service ClusterIP/ports.
     - Copies referenced TLS CA secrets.
5. Aggregates copy errors and returns combined error.

Watch details:

- `Named("servicemonitor-federation")`.
- `For(ServiceMonitor)` with predicate `.../prometheus=federated`.
- Watches `Service` and requeues matching federated ServiceMonitors.
- Watches `Prometheus` and requeues all matching federated ServiceMonitors.

### namespace.ClientOrgReconciler

Scope: Namespaces with `kmapi.ClientOrgKey=true` and `kmapi.ClientOrgMonitoringKey!=false`.

Primary behavior:

1. Validates required client-org annotation (`kmapi.AceOrgIDKey`) and detector readiness.
2. Fails fast for unsupported Rancher + federated combination.
3. Adds finalizer `monitoring.appscode.com/prometheus`.
4. On deletion:
   - Unregisters client-org backend context.
   - Removes finalizer.
5. Ensures monitoring namespace `<namespace>-monitoring`.
6. Ensures rolebinding `appscode:client-org:monitoring` in monitoring namespace.
7. Verifies trickster registration marker exists before proceeding.
8. Registers backend context (issue token mode), then creates/updates:
   - AppBinding `grafana` in client monitoring namespace.
   - Secret `grafana-auth`.
9. Copies all GrafanaDashboard objects into client monitoring namespace and transforms:
   - dashboard title (`ClientDashboardTitle`).
   - templating variable `namespace` as constant client namespace value.
   - grafanaRef to local appbinding.
   - source/hash annotations for traceability.

Watch details:

- `Named("namespace")`.
- `For(Namespace)` with client-org predicate.
- Watches `ServiceAccount trickster` with registration label and requeues all client-org namespaces.

### ranchertoken.TokenRefresher

Primary behavior:

1. Watches only the configured Rancher auth secret in pod namespace.
2. Parses token using cluster state aware lookup.
3. Renews token if expiry is within 24 hours.
4. Requeues at `next_expiry - 24h`.

### openviz.GrafanaDashboardReconciler

Primary behavior:

1. Ensures finalizer `grafanadashboard.openviz.dev/finalizer`.
2. On delete:
   - Sets terminating phase.
   - Deletes remote dashboard by UID (treats 404 as already deleted).
   - Removes finalizer.
3. On active objects:
   - Sets processing phase (unless already failed).
   - Resolves Grafana AppBinding and computed Grafana state hash.
   - Optionally templatizes datasource references in dashboard JSON.
   - Upserts dashboard via Grafana API.
   - Patches status dashboard reference, state, phase, reason, and ready condition.
4. On failures:
   - Records warning events.
   - Marks failed condition and reason.
   - Requeues using configurable backoff (`RequeueAfterDuration`).

Watch details:

- `For(GrafanaDashboard)` with predicate:
  - first reconcile always,
  - later reconcile when associated Grafana state changed.
- Watches `AppBinding` and requeues dashboards that reference that appbinding and are failed/stale.

### openviz.GrafanaDatasourceReconciler

Primary behavior:

1. Ensures finalizer `grafanadatasource.openviz.dev/finalizer`.
2. On delete:
   - Sets terminating phase.
   - Deletes remote datasource when status has remote datasource ID.
   - Removes finalizer.
3. On first active reconcile:
   - Initializes processing status and clears conditions.
   - Creates datasource if status ID absent, else updates existing datasource.
   - Patches current phase/reason/observed generation on success.
4. On failures:
   - Emits warning event.
   - Patches failed phase/reason and condition.

Watch details:

- `For(GrafanaDatasource)`.

## Utility Files in This Directory

- `clientorg/clientorg.go`: computes per-client monitoring namespace names.
- `config.go`: ensures CRDs for GrafanaDashboard, GrafanaDatasource, and AppBinding.