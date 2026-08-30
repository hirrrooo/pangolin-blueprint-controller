# Pangolin Blueprint Controller

[![CI](https://github.com/hirrrooo/pangolin-blueprint-controller/actions/workflows/ci.yaml/badge.svg)](https://github.com/hirrrooo/pangolin-blueprint-controller/actions/workflows/ci.yaml)
[![GitHub release](https://img.shields.io/github/v/release/hirrrooo/pangolin-blueprint-controller)](https://github.com/hirrrooo/pangolin-blueprint-controller/releases)
[![License](https://img.shields.io/github/license/hirrrooo/pangolin-blueprint-controller)](LICENSE)

A Kubernetes sidecar that turns annotated Services into a continuously managed [Pangolin Blueprint](https://docs.pangolin.net/manage/blueprints). It runs beside Newt, watches Services across the cluster, and atomically updates the blueprint file Newt applies to Pangolin.

> [!IMPORTANT]
> This is an independent community project. It is not affiliated with or endorsed by Fossorial or Pangolin.

The project is in the `v0.x` release series. Its API and annotation contract are usable, but review release notes and validate upgrades in a non-production namespace.

## Compatibility

| Component | Support |
| --- | --- |
| Kubernetes | 1.30 or newer |
| Newt | Sample manifest pins 1.16.0 |
| Pangolin modes | HTTP, TCP, UDP |
| Container platforms | Linux AMD64 and ARM64 |

## Quick start

Create the Newt credentials expected by the sample deployment (see `deploy/secrets.example.yaml` for a ready-to-use template):

```sh
kubectl create namespace pangolin
kubectl create secret generic newt-auth \
  --namespace pangolin \
  --from-literal=PANGOLIN_ENDPOINT=https://app.pangolin.net \
  --from-literal=NEWT_ID='<newt-id>' \
  --from-literal=NEWT_SECRET='<newt-secret>'
```

Review `deploy/deployment.yaml`, select a released controller image tag or digest, and apply it:

```sh
kubectl apply -f deploy/deployment.yaml
kubectl rollout status -n pangolin deploy/pangolin-newt
kubectl logs -n pangolin deploy/pangolin-newt -c blueprint-controller -f
```

## How the deployment works

```text
Kubernetes Services ───── cluster-wide list/watch ────┐
                                                      ▼
Policy Secrets ── namespace + label scoped watch ──► informer caches
                                                      │ cached events
                                                      ▼
                                           debounced full reconciliation
                                                      │ validate references
                                                      ▼
                                  Pangolin public-resources + public-policies
        │ deterministic YAML marshal
        ▼
atomic rename into memory-backed emptyDir
        │ shared read-only mount
        ▼
Newt --blueprint-file
        │ continuous blueprint apply
        ▼
External Pangolin instance
```

The controller never calls Pangolin directly and does not need Pangolin credentials. Newt owns the control-plane connection; the controller owns only `/var/run/pangolin/blueprint.yaml`.

### Pod composition

`deploy/deployment.yaml` creates one Pod with:

1. `initialize-blueprint`, an init container that writes `public-resources: {}` before Newt starts.
2. `newt`, which receives `PANGOLIN_ENDPOINT`, `NEWT_ID`, and `NEWT_SECRET` from the existing `newt-auth` Secret.
3. `blueprint-controller`, which watches Services and policy Secrets, then writes the blueprint.

The containers share a memory-backed `emptyDir`:

```yaml
volumes:
  - name: blueprint
    emptyDir:
      medium: Memory
```

Kubernetes implements this as tmpfs, so frequent blueprint replacements do not consume SSD, NVMe, or flash write cycles. The contents are intentionally ephemeral and rebuilt from the informer cache whenever the Pod starts. Newt mounts the volume read-only; only the init container and controller can write it.

### Kubernetes permissions

The controller's ClusterRole grants only `get`, `list`, and `watch` on core Services. A separate Role grants only `list` and `watch` on Secrets in `pangolin-policies`. Pod-level automatic token mounting is disabled. A projected, rotating ServiceAccount token is mounted only into `blueprint-controller`, so Newt does not receive Kubernetes API credentials.

### Startup and readiness

The init container first creates a valid empty blueprint. The controller then:

1. Builds an in-cluster Kubernetes client.
2. Starts a cluster-wide Service informer and namespace-scoped policy Secret informer.
3. Waits for both informer caches to synchronize.
4. Reconciles the complete cached Service and referenced-policy state immediately.
5. Marks `/readyz` ready only after the initial blueprint is written successfully.

`/healthz` reports process liveness. The Deployment uses both endpoints as container probes.

## Reconciliation model

Informer event handlers react to Service and policy Secret additions, updates, and deletions. They send lightweight signals into a buffered channel; they do not write files or make direct Secret lookups.

The controller waits for a configurable quiet period before reconciling:

```text
--debounce=750ms
```

Each new event resets the timer. A GitOps operation that updates many Services therefore produces one complete reconciliation instead of one write per Service.

Reconciliation always rebuilds the entire desired blueprint from the informer cache. This gives deletion and opt-out behavior without maintaining a separate database:

- Add an opted-in Service: its resource appears.
- Update its annotations or ports: its resource changes.
- Delete the Service: its resource disappears.
- Remove `pangolin.net/public: "true"`: its resource disappears.
- Make an existing Service invalid: its resource is omitted and a warning explains why.

Invalid Services do not block valid Services. Duplicate resource IDs fail closed: the conflicting resource is omitted rather than allowing one Service to silently take ownership.

## Service annotations

A Service participates only when it has exactly:

```yaml
pangolin.net/public: "true"
```

| Annotation | Required | Meaning |
| --- | --- | --- |
| `pangolin.net/public` | Yes | Must equal `"true"` to opt in. |
| `pangolin.net/domain` | HTTP | Public fully qualified domain name, without `https://`. |
| `pangolin.net/mode` | No | `http` by default; also supports `tcp` and `udp`. |
| `pangolin.net/port` | Sometimes | Service port name or numeric `spec.ports[].port`. May be omitted only when the Service has exactly one port. |
| `pangolin.net/proxy-port` | TCP/UDP | Public Pangolin server port from 1 through 65535. |
| `pangolin.net/method` | No | HTTP upstream method: `http` by default, `https`, or `h2c`. |
| `pangolin.net/resource-id` | No | Stable Pangolin `niceId`. Defaults to `<namespace>--<service>` and must be unique cluster-wide. |
| `pangolin.net/name` | No | Display name. Defaults to `<namespace>/<service>`. |
| `pangolin.net/policy` | HTTP | Name of a reusable public-policy Secret in the configured policy namespace. |
| `pangolin.net/sso-enabled` | No | HTTP authentication boolean. |
| `pangolin.net/sso-roles` | No | Comma-separated Pangolin role names. |
| `pangolin.net/sso-users` | No | Comma-separated Pangolin user identifiers. |
| `pangolin.net/whitelist-users` | No | Comma-separated email addresses or patterns. |
| `pangolin.net/rules` | No | JSON array of Pangolin access rules. |

Domain and method remain Service annotations because they describe HTTP routing. Policy, auth, rules, domain, and method annotations are rejected for TCP and UDP resources. Passwords, PINs, basic-auth credentials, and Newt credentials are never accepted in annotations.

## Secret-backed reusable public policies

Set `pangolin.net/policy` on an HTTP Service to reference a purpose-built Secret. The Secret name also becomes the Pangolin `public-policies` key and the resource's `policy` reference:

```yaml
metadata:
  annotations:
    pangolin.net/public: "true"
    pangolin.net/domain: dashboard.example.com
    pangolin.net/policy: authenticated-members
```

Policies are read only from `--policy-namespace`, which defaults to `pangolin-policies`. Services cannot select another namespace. The Deployment grants `list` and `watch` on Secrets through a namespaced Role; the cluster-wide ClusterRole still covers Services only.

A policy Secret must have the policy label and custom type:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: authenticated-members
  namespace: pangolin-policies
  labels:
    pangolin.net/public-policy: "true"
type: pangolin.net/public-policy
stringData:
  name: Authenticated members
  sso: "true"
  sso-roles: '["Member","Platform"]'
  sso-users: '[]'
  password: replace-with-a-secret
  pincode: "012345"
  basic-auth-user: demo
  basic-auth-password: replace-with-a-secret
  basic-auth-extended-compatibility: "true"
  email-whitelist-enabled: "true"
  whitelist-users: '["*@example.com"]'
  apply-rules: "true"
  rules: >-
    [{"action":"deny","match":"country","value":"XX","enabled":true}]
```

Only `name` is required. Omitted `sso` follows Pangolin's `true` default. Optional fields are validated as follows:

| Secret key | Format |
| --- | --- |
| `name` | Non-empty display name |
| `sso` | `true` or `false` |
| `auto-login-idp` | Positive integer |
| `sso-roles` | JSON string array; `Admin` is rejected |
| `sso-users` | JSON string array |
| `password` | Non-empty string |
| `pincode` | Exactly six digits |
| `basic-auth-user` / `basic-auth-password` | Both must be present and non-empty |
| `basic-auth-extended-compatibility` | Boolean; valid only with a complete basic-auth pair |
| `email-whitelist-enabled` | Boolean |
| `whitelist-users` | JSON string array; requires email whitelist to be enabled |
| `apply-rules` | Boolean |
| `rules` | JSON rule array; requires `apply-rules: "true"` |

Do not combine `pangolin.net/policy` with direct SSO or rules annotations. The controller rejects ambiguous combinations instead of merging them.

Only valid policies referenced by valid Services are written to `public-policies`. A missing, invalid, relabeled, or deleted policy causes every referencing resource to be omitted. The controller never falls back to an unprotected resource. Secret creation, updates, credential rotation, label changes, and deletion all trigger the same debounced reconciliation used for Service changes.

The generated blueprint necessarily contains the policy credentials so Newt can apply them. It remains on the memory-backed `emptyDir` and is atomically replaced. Credentials and complete blueprints are never logged.

### External Secrets

External Secrets can materialize the required Secret without storing credentials in Git:

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: authenticated-members
  namespace: pangolin-policies
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: production
    kind: ClusterSecretStore
  target:
    name: authenticated-members
    creationPolicy: Owner
    template:
      type: pangolin.net/public-policy
      metadata:
        labels:
          pangolin.net/public-policy: "true"
      data:
        name: Authenticated members
        sso: "true"
        password: "{{ .password }}"
  data:
    - secretKey: password
      remoteRef:
        key: pangolin/policies/authenticated-members
        property: password
```

External Secrets rotation updates the target Secret and triggers reconciliation without restarting the Pod.

### SOPS

Create a normal policy Secret, encrypt only its data, and commit the encrypted result:

```sh
sops --encrypt \
  --encrypted-regex '^(data|stringData)$' \
  policy-secret.yaml > policy-secret.enc.yaml
```

Flux or Argo CD can decrypt it into `pangolin-policies`. Never commit the decrypted Secret.

### Safe migration

Existing Services without `pangolin.net/policy` keep their current behavior. For a protected rollout:

1. Deploy the controller and namespaced policy RBAC.
2. Create or synchronize the policy Secret.
3. Confirm it has the required label and type.
4. Add `pangolin.net/policy` to the Service.
5. Follow controller logs until the protected resource is applied.

Adding the annotation before a valid Secret exists removes the resource from the generated blueprint. Deleting or invalidating a policy does the same. Removing the annotation is security-sensitive because the Service returns to the direct annotation path; disable public exposure before removing or replacing a policy.

### Port resolution

When `pangolin.net/port` is a name, the controller finds the matching `spec.ports[].name`. When it is numeric, it must match an actual `spec.ports[].port`. The generated target uses the Service port, not the Pod `targetPort`.

Every target hostname is:

```text
<service>.<namespace>.svc.cluster.local
```

Kubernetes Service routing then forwards the connection to the selected Pods and `targetPort`.

## HTTP example

```yaml
apiVersion: v1
kind: Service
metadata:
  name: dashboard
  namespace: apps
  annotations:
    pangolin.net/public: "true"
    pangolin.net/domain: dashboard.example.com
    pangolin.net/port: web
    pangolin.net/method: http
    pangolin.net/sso-enabled: "true"
    pangolin.net/sso-roles: Member,Platform
    pangolin.net/rules: >-
      [{"action":"deny","match":"path","value":"/admin","priority":10}]
spec:
  selector:
    app: dashboard
  ports:
    - name: web
      port: 80
      targetPort: 3000
```

Generated resource:

```yaml
public-resources:
  apps--dashboard:
    name: apps/dashboard
    mode: http
    full-domain: dashboard.example.com
    auth:
      sso-enabled: true
      sso-roles:
        - Member
        - Platform
    rules:
      - action: deny
        match: path
        value: /admin
        priority: 10
    targets:
      - hostname: dashboard.apps.svc.cluster.local
        port: 80
        method: http
```

The target omits `site`. When Newt applies a blueprint, Pangolin automatically assigns targets without `site` to that Newt's site.

Rule actions are `allow`, `deny`, or `pass`. Match types are `cidr`, `path`, `ip`, `country`, `asn`, or `region`.

## TCP and UDP example

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mqtt
  namespace: apps
  annotations:
    pangolin.net/public: "true"
    pangolin.net/mode: tcp
    pangolin.net/port: mqtt
    pangolin.net/proxy-port: "1883"
spec:
  selector:
    app: mqtt
  ports:
    - name: mqtt
      port: 1883
      targetPort: 1883
```

Generated resource:

```yaml
public-resources:
  apps--mqtt:
    name: apps/mqtt
    mode: tcp
    proxy-port: 1883
    targets:
      - hostname: mqtt.apps.svc.cluster.local
        port: 1883
```

Raw targets omit `method`, and Pangolin does not permit HTTP auth on them.

## Atomic file updates

The controller never truncates the active file. `internal/atomicfile` performs this sequence:

1. Compare the generated bytes with the current file and return if unchanged.
2. Create a temporary file in the same directory.
3. Set mode `0644` so Newt can read it under a different UID.
4. Write and synchronize the complete YAML.
5. Rename the temporary file over `blueprint.yaml`.
6. Synchronize the containing directory.

The same-filesystem rename is atomic. Newt sees either the complete old blueprint or the complete new blueprint, never partially written YAML.

## Logs and troubleshooting

Logs are structured JSON with stable `component`, `namespace`, and `service` fields. Follow the controller:

```sh
kubectl logs -n pangolin deploy/pangolin-newt -c blueprint-controller -f
```

An invalid port includes the available Service ports:

```json
{"level":"WARN","msg":"skipping invalid Service","component":"blueprint","namespace":"apps","service":"plex","reason":"port \"web\" does not match a Service port name; available ports: \"http\" (80), \"pms\" (32400)"}
```

A resource-ID collision names both Services:

```json
{"level":"WARN","msg":"blueprint resource ID collision","component":"blueprint","namespace":"apps","service":"homer","resource_id":"dashboard","conflicts_with":"apps/grafana"}
```

Successful changed writes report resource count and output size. Unchanged reconciliations are logged only at debug level. To enable debug logs, add the argument to the controller container and roll out the Deployment:

```yaml
args:
  - --log-level=debug
```

Common checks:

```sh
# Controller health and informer readiness
kubectl get pod -n pangolin -l app.kubernetes.io/name=pangolin-newt

# Follow reconciliation and validation messages
kubectl logs -n pangolin deploy/pangolin-newt -c blueprint-controller -f

# Show warnings for omitted Services
kubectl logs -n pangolin deploy/pangolin-newt -c blueprint-controller | grep '"level":"WARN"'
```

The controller image is distroless and has no shell utilities. Use the structured logs as the primary diagnostic surface.

## Security boundary

Anyone allowed to annotate Services can request public exposure through this controller. Treat Service mutation as exposure authority. Restrict it with namespace RBAC, `ValidatingAdmissionPolicy`, Kyverno, or Gatekeeper in multi-tenant clusters.

The controller watches all namespaces by design. It does not write Kubernetes status fields or Events, so warning logs are the authoritative explanation for omitted routes.

Kubernetes RBAC cannot restrict Secret `list` and `watch` by label. The dedicated policy namespace is therefore the authorization boundary and must contain only purpose-built policy Secrets. The controller applies a server-side policy label selector and validates Secret type at runtime. Keep `newt-auth` in `pangolin`; the reserved name is rejected as a policy.

## Build, test, and deploy

```sh
go test -race ./...
docker build -t pangolin-blueprint-controller .
```

Runtime options:

```text
-output string          blueprint path (default /var/run/pangolin/blueprint.yaml)
-debounce duration      reconciliation quiet period (default 750ms)
-kubeconfig string      local kubeconfig; empty uses in-cluster credentials
-policy-namespace string namespace containing policy Secrets (default pangolin-policies)
-health-address string  health listener (default :8080)
-log-level string       debug, info, warn, or error
```

Deployment steps:

1. Build and publish the controller image.
2. Replace the example version in `deploy/deployment.yaml` with the release tag or immutable digest you want to deploy.
3. Create namespace `pangolin`.
4. Create Secret `newt-auth` in namespace `pangolin` (see `deploy/secrets.example.yaml`).
5. (Optional) Create policy Secrets in `pangolin-policies` if using `pangolin.net/policy`.
6. Apply the manifest:

```sh
kubectl apply -f deploy/deployment.yaml
```

The generated Go types cover the public-resource and reusable public-policy fields owned by this controller. Unsupported Pangolin modes and fields fail closed rather than producing speculative configuration.
