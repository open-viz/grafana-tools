# Detect DashboardLink for Resource

I think I have a clear idea how to solve the Grafana AppBinding / Datasource / Dashboard connection issues. This is how:

- There will be a "default" Grafana AppBinding. This will be done via an annotation (or label?) on an AppBinding created from grafana-configurator chart. This will be similar to how the default storageclass is defined. `{"openviz.dev/is-default-grafana":"true"}`

  - Q: How do we enforce there is more than one?

    A: One option is to use a validating webhook for AppBinding.

    I think the better option will be to just error out if we can't determine a single default AppBinding.

    In this default AppBinding, we will also store the Datasource and FolderID in the params (already done).

- GrafanaDashboards can avoid setting the GrafanaRef (which is namespace/name). If not set, then the default Grafana AppBinding will be used (could be cross namespace). This is what the Kubedb-grafana-dashboard charts to by default.

  - Q: We have a double opt-in type problem. How do we know Grafana AppBinding wants these Dashbaords installed there?

    A: We can extend the config in AppBinding params to include double opt-in usage policy fields.

- DashboardLinks will refer to a Dashboard via only its title or (namespace/name). If only title is defined, then we find the GrafanaDashbaord that uses the default Grafana AppBinding  with that title and use that. 

- Resource-metadata YAMLs will be extended to provide the full API object for these calls. We already have a way to define Query for the GraphQL stuff. So, we can just extend it further using a requestTemplate that is rendered using the Target object (say, database).
