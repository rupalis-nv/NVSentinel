# Preflight configuration

Preflight is a mutating admission webhook that injects GPU diagnostic init containers (DCGM diagnostics, optional NCCL loopback / all-reduce) into pods that request GPUs in namespaces you opt in via labels. It answers "is this GPU healthy enough to start?" before your workload runs—separate from continuous [GPU health monitoring](../gpu-health-monitor.md).

For architecture, tradeoffs, and metrics design, see [ADR-026: Preflight checks](../designs/026-preflight-checks.md).

## Prerequisites

- Helm subchart is off by default; enable with `global.preflight.enabled` (see below).
- cert-manager (or OpenShift service CA) for webhook TLS—same expectation as the rest of the NVSentinel chart.
- DCGM reachable from injected init containers (typically the NVIDIA GPU Operator's DCGM / hostengine service). Configure the endpoint via `DCGM_HOSTENGINE_ADDR` on the `preflight-dcgm-diag` init container.
- Multi-node / gang checks (e.g. `preflight-nccl-allreduce`): enable gang coordination and configure gang discovery for your scheduler (see below).

## Enable preflight

1. Set the global flag:

```yaml
global:
  preflight:
    enabled: true
```

2. Configure the preflight subchart under the top-level `preflight:` key (values merge into `distros/kubernetes/nvsentinel/charts/preflight/values.yaml`). At minimum, review `initContainers` (including DCGM and NCCL env vars) and `webhook.failurePolicy`.

3. Label namespaces where injection should apply:

```bash
kubectl label namespace <namespace> nvsentinel.nvidia.com/preflight=enabled
```

The chart default `namespaceSelector` matches that label.

## Init container placement

By default the webhook **appends** preflight init containers after any existing init containers in the pod spec. This ensures provider-injected setup containers (e.g., GCP TCPXO daemon) complete before preflight checks run.

Set `initContainerPlacement` to change this behavior:

```yaml
# "append" (default): add after existing init containers
# "prepend": add before existing init containers
initContainerPlacement: "prepend"
```

Use `prepend` when preflight checks must run before other init containers — for example, to gate workload setup on GPU health validation.

## Per-pod check selection

By default, all init containers with `defaultEnabled: true` (or omitted, which defaults to true) are injected into every GPU pod. To select a subset of checks for a specific pod, annotate it:

```yaml
metadata:
  annotations:
    nvsentinel.nvidia.com/preflight-checks: "preflight-dcgm-diag,preflight-nccl-loopback"
```

Only the named containers are injected, in the order they appear in the annotation. Duplicate or unknown container names reject admission with an error.

An empty value disables all checks:

```yaml
nvsentinel.nvidia.com/preflight-checks: ""
```

When the annotation is absent, `defaultEnabled` on each init container controls whether it runs. For gang-aware checks (`nccl-allreduce`), all pods in the gang must have the same annotation value — mismatches are detected and fail fast before torchrun launches.

See [ADR-034](../designs/034-preflight-check-selection.md) for design details.

## Init containers (check configuration)

The `initContainers` list in the preflight chart defines which checks the webhook injects. Each entry is a standard `corev1.Container` plus preflight-specific controls such as `defaultEnabled`, `inheritUserEnv`, and `inheritUserVolumeMounts` — you control images, env vars, resource limits, security contexts, and volume mounts.

The webhook automatically injects these env vars into every init container (you do not need to set them):

| Env var | Source | Purpose |
|---------|--------|---------|
| `NODE_NAME` | Downward API (`spec.nodeName`) | Kubernetes node name for health events |
| `PLATFORM_CONNECTOR_SOCKET` | Chart `connectorSocket` | Unix socket for the platform-connector gRPC endpoint |
| `PROCESSING_STRATEGY` | Chart `processingStrategy` | `EXECUTE_REMEDIATION` or `STORE_ONLY` — controls downstream action |

For gang-aware containers the webhook also injects `GANG_ID`, `GANG_CONFIG_DIR`, `GANG_TIMEOUT_SECONDS`, and `POD_NAME`.

By default, the built-in checks use curated environments and do not inherit matching env vars or volume mounts from workload containers. To intentionally mirror workload NCCL/fabric configuration for a specific check, set `inheritUserEnv: true` and/or `inheritUserVolumeMounts: true` on that `initContainers` entry.

### preflight-dcgm-diag

Runs DCGM diagnostics against every GPU allocated to the pod via the remote hostengine.

| Env var | Default | Description |
|---------|---------|-------------|
| `DCGM_DIAG_LEVEL` | `2` | Diagnostic depth: 1 = short (approx 30 s, software deployment checks), 2 = medium (approx 2 min, adds PCIe and basic GPU stress), 3 = long (approx 15 min, adds Diagnostic plugin stress), 4 = xlong (1-2 hr, extended stress) |
| `DCGM_HOSTENGINE_ADDR` | `nvidia-dcgm.gpu-operator.svc:5555` | DCGM hostengine gRPC endpoint |
| `DCGM_DIAG_STATUS_RETRY_MAX_ATTEMPTS` | `10` | Maximum diagnostic attempts when DCGM returns a `DCGM_ST_*` status while starting/running diagnostics |
| `DCGM_DIAG_STATUS_RETRY_INTERVAL_SECONDS` | `10` | Delay between `DCGM_ST_*` retry attempts |

Example values override:

```yaml
initContainers:
  - name: preflight-dcgm-diag
    image:
      repository: ghcr.io/nvidia/nvsentinel/preflight-dcgm-diag
      tag: ""
    env:
      - name: DCGM_HOSTENGINE_ADDR
        value: "nvidia-dcgm.gpu-operator.svc:5555"
      - name: DCGM_DIAG_LEVEL
        value: "2"
      - name: DCGM_DIAG_STATUS_RETRY_MAX_ATTEMPTS
        value: "10"
      - name: DCGM_DIAG_STATUS_RETRY_INTERVAL_SECONDS
        value: "10"
    volumeMounts:
      - name: nvsentinel-socket
        mountPath: /var/run
```

If a `DCGM_ST_*` status still prevents the diagnostic from completing after
retries, `preflight-dcgm-diag` emits a non-fatal unhealthy HealthEvent with
`RecommendedAction=NONE` and exits successfully so the workload is not blocked by
a preflight infrastructure failure.

### preflight-nccl-loopback

Single-node NCCL all-reduce across all GPUs on the node. Validates intra-node interconnect (NVLink or PCIe).

| Env var | Default | Description |
|---------|---------|-------------|
| `BW_THRESHOLD_GBPS` | `150` | Minimum acceptable bus bandwidth in GB/s. NVLink interconnect typically sustains 150+ GB/s; set to approx 15 GB/s for PCIe interconnect |
| `TEST_SIZE_MB` | `256` | Message size in MB for the all-reduce benchmark |
| `SKIP_BANDWIDTH_CHECK` | `false` | When `true`, pass if the benchmark completes regardless of measured bandwidth |

Example values override:

```yaml
initContainers:
  - name: preflight-nccl-loopback
    image: ghcr.io/nvidia/nvsentinel/preflight-nccl-loopback:latest
    env:
      - name: BW_THRESHOLD_GBPS
        value: "15"           # PCIe interconnect
      - name: TEST_SIZE_MB
        value: "512"
```

### preflight-nccl-allreduce

Multi-node NCCL all-reduce across the entire gang. Requires `gangCoordination.enabled: true` and a gang-aware scheduler.

| Env var | Default | Description |
|---------|---------|-------------|
| `BW_THRESHOLD_GBPS` | `100` | Minimum acceptable bus bandwidth in GB/s |
| `MESSAGE_SIZES` | `4G` | Comma-separated message sizes for the benchmark (e.g. `"4G"`, `"4G,8G"`). Code default is `4G,8G`; Helm chart overrides to `4G` |
| `BENCHMARK_ITERS` | `20` | Number of timed iterations per message size |
| `WARMUP_ITERS` | `5` | Warmup iterations before timing begins |
| `NCCL_REDUCE_OP` | `sum` | Reduction operation (`sum`, `prod`, `min`, `max`, `avg`) |
| `SKIP_BANDWIDTH_CHECK` | `false` | Pass if benchmark completes regardless of bandwidth |
| `NCCL_DEBUG` | — | NCCL log verbosity (`INFO`, `WARN`, etc.) |
| `NCCL_DEBUG_SUBSYS` | — | NCCL subsystems to log (`INIT,NET`, etc.) |

The container also requires `IPC_LOCK` capability for RDMA memory registration:

```yaml
initContainers:
  - name: preflight-nccl-allreduce
    image: ghcr.io/nvidia/nvsentinel/preflight-nccl-allreduce:latest
    securityContext:
      capabilities:
        add: ["IPC_LOCK"]
    env:
      - name: BW_THRESHOLD_GBPS
        value: "100"
      - name: MESSAGE_SIZES
        value: "4G"
```

### Fabric-specific NCCL configuration

When a check opts in with `inheritUserEnv` or `inheritUserVolumeMounts`, the webhook copies matching NCCL env vars and volume mounts from the pod's main containers using glob patterns:

```yaml
ncclEnvPatterns:    ["NCCL_*", "FI_*", "LD_LIBRARY_PATH", "UCX_*", "TORCH_NCCL_*", "CUDA_DEVICE_ORDER"]
volumeMountPatterns: ["host-opt-amazon*", "nvtcpxo-*", "nccl-*", "dev-shm"]
```

This means if your training container already has the correct `NCCL_TOPO_FILE`, `FI_PROVIDER`, or `LD_LIBRARY_PATH`, an opted-in preflight init container can inherit them with no manual configuration.
Inheritance is per init container. The built-in checks set `inheritUserEnv: false` and `inheritUserVolumeMounts: false` by default to avoid workload-specific NCCL tuning poisoning preflight checks. Enable the flags only for checks that should intentionally mirror the workload environment:

```yaml
initContainers:
  - name: preflight-nccl-allreduce
    inheritUserEnv: true
    inheritUserVolumeMounts: true
```

For standalone testing (e.g. busybox main container), use `ncclAllreduceExtraEnv` and `gangCoordination.extraHostPathMounts` to provide fabric config explicitly.

## Gang discovery

Gang discovery identifies pods that belong to the same scheduling group so multi-node preflight checks (NCCL all-reduce) know their peers. A pod carries a "gang anchor"—a reference to a parent object—that holds gang metadata such as the minimum member count.

Two discovery mechanisms are supported:

### Native Kubernetes: schedulingGroup / workloadRef

The default when `gangDiscovery` is left empty (`{}`). Preflight first uses the Kubernetes 1.36 native PodGroup API when available, then falls back to the Kubernetes 1.35 native Workload API.

> The `PodGroup` resource (`scheduling.k8s.io/v1alpha2`) and `spec.schedulingGroup` are alpha in Kubernetes 1.36 and disabled by default. Enable the `GenericWorkload` feature gate on the API server and scheduler to use this path.

In Kubernetes 1.36, each pod links to a `PodGroup` resource via `spec.schedulingGroup`:

```yaml
spec:
  schedulingGroup:
    podGroupName: training-workers
```

The PodGroup object contains a gang policy with `minCount`:

```yaml
apiVersion: scheduling.k8s.io/v1alpha2
kind: PodGroup
metadata:
  name: training-workers
spec:
  schedulingPolicy:
    gang:
      minCount: 2
```

No `gangDiscovery` configuration is needed for this path.

In Kubernetes 1.35, each pod links to a native `Workload` resource via `spec.workloadRef`:

```yaml
spec:
  workloadRef:
    name: training-job-workload
    podGroup: workers
```

The Workload object contains pod groups and gang policy:

```yaml
apiVersion: scheduling.k8s.io/v1alpha1
kind: Workload
metadata:
  name: training-job-workload
spec:
  podGroups:
    - name: workers
      policy:
        gang:
          minCount: 2
```

No `gangDiscovery` configuration is needed for this fallback path either.

The default chart RBAC grants read access to both native resources:
`scheduling.k8s.io/podgroups` for Kubernetes 1.36 and
`scheduling.k8s.io/workloads` for Kubernetes 1.35.

### PodGroup-based schedulers (Volcano, Run:ai / OSMO, and similar)

For schedulers that use PodGroup CRDs, configure `gangDiscovery` with:

| Field | Purpose |
|-------|---------|
| `name` | Discoverer identifier, used in the gang ID prefix and logging (e.g. `"volcano"`) |
| `annotationKeys` | Pod annotation keys checked (in order) for the PodGroup name |
| `labelKeys` | Optional pod label keys checked as fallback |
| `podGroupGVR` | `group`, `version`, `resource` of the PodGroup CRD |
| `minCountExpr` | CEL expression to extract the minimum member count from the PodGroup object. Receives `podGroup` as the unstructured object. Default: `"podGroup.spec.minMember"` |

Volcano example:

```yaml
gangDiscovery:
  name: "volcano"
  annotationKeys:
    - "scheduling.k8s.io/group-name"
  podGroupGVR:
    group: "scheduling.volcano.sh"
    version: "v1beta1"
    resource: "podgroups"
  minCountExpr: "podGroup.spec.minMember"
```

Volcano sets the `scheduling.k8s.io/group-name` annotation on each pod. The discoverer reads that annotation, fetches the corresponding `PodGroup` CRD, and evaluates `minCountExpr` to determine expected gang size.

OSMO + Kai scheduler example:

```yaml
gangDiscovery:
  name: "osmo-with-kai"
  labelKeys:
    - "osmo.group_uuid"
  podGroupGVR:
    group: "scheduling.run.ai"
    version: "v2alpha2"
    resource: "podgroups"
  minCountExpr: "podGroup.spec.minMember"
```

Here membership is determined by a pod label instead of an annotation. The rest of the flow is the same: look up the PodGroup CRD and extract `minCount` via CEL.

### OSMO + KAI Scheduler

[KAI Scheduler](https://github.com/kai-scheduler/KAI-Scheduler) uses the `scheduling.run.ai/v2alpha2` PodGroup CRD. In NVIDIA DGX Cloud deployments, KAI runs with **OSMO**: OSMO creates the PodGroup and labels each pod with `osmo.group_uuid`. Preflight reads that label, fetches the PodGroup, and uses `spec.minMember` as the expected gang size for `preflight-nccl-allreduce`.

#### Step 1 — Enable preflight cluster-wide

```yaml
global:
  preflight:
    enabled: true

preflight:
  gangDiscovery:
    name: "osmo-with-kai"
    labelKeys:
      - "osmo.group_uuid"
    podGroupGVR:
      group: "scheduling.run.ai"
      version: "v2alpha2"
      resource: "podgroups"
    minCountExpr: "podGroup.spec.minMember"
```

The chart's built-in RBAC contributor role grants read access to `scheduling.run.ai/podgroups` when `gangDiscovery.podGroupGVR` is set in Helm values (see [RBAC (aggregated ClusterRole)](#rbac-aggregated-clusterrole) below).

#### Step 2 — Label namespaces for injection

```bash
kubectl label namespace <training-namespace> nvsentinel.nvidia.com/preflight=enabled
```

#### Step 3 — Submit a gang-scheduled workload

OSMO creates the PodGroup and labels each pod in the gang with `osmo.group_uuid`. Verify membership and the PodGroup before checking gang coordination:

```bash
kubectl -n <training-namespace> get pod <pod-name> -o jsonpath='{.metadata.labels.osmo\.group_uuid}{"\n"}'
kubectl -n <training-namespace> get podgroups.scheduling.run.ai
```

The gang ID is `<gangDiscovery.name>-<namespace>-<podGroupName>`. For example, with `name: osmo-with-kai` in namespace `team-a` and PodGroup `myjob`, the gang ID is `osmo-with-kai-team-a-myjob`.

> If the PodGroup does not exist yet, preflight still creates the gang ConfigMap at admission but records `expected_count: 0` until a PodGroup with `minMember` is found.

#### Step 4 — Verify gang coordination (multi-node checks only)

When `preflight-nccl-allreduce` is enabled, confirm the gang ConfigMap is created and populated:

```bash
kubectl -n <training-namespace> get configmap -l nvsentinel.nvidia.com/managed-by=preflight -o yaml
```

The ConfigMap should contain `expected_count`, `peers`, `master_addr`, and `gang_id` before the NCCL init container completes. The ConfigMap name starts with `preflight-`, but long gang IDs are sanitized and truncated with a hash suffix, so use the label selector above instead of constructing the name by hand.

### Grove

[Grove](https://github.com/ai-dynamo/grove) orchestrates multi-component AI inference workloads and relies on **KAI Scheduler** for gang scheduling. Grove's operator translates a `PodCliqueSet` into `PodGang` resources; KAI then creates or resolves the corresponding `scheduling.run.ai/v2alpha2` PodGroups.

From preflight's perspective, Grove workloads use the **same OSMO + KAI `gangDiscovery` configuration** — there is no Grove-specific CRD to configure. Set up preflight exactly as in the [OSMO + KAI Scheduler](#osmo--kai-scheduler) section, then label the Grove workload namespace:

```bash
kubectl label namespace <grove-namespace> nvsentinel.nvidia.com/preflight=enabled
```

Verify that pods carry the `osmo.group_uuid` label and that a PodGroup exists, then confirm the gang ConfigMap as in OSMO + KAI Step 4. The gang ID follows the same `<gangDiscovery.name>-<namespace>-<podGroupName>` form.

### Per-namespace gang discovery

The Helm `gangDiscovery` value sets only the **cluster-wide default**. To make a specific namespace use a different gang-scheduling system (for example, Volcano for one team while everyone else uses native Kubernetes), create a **`PreflightConfig`** custom resource in that namespace. It is reconciled at runtime — no Helm upgrade and no controller restart.

A pod is resolved as follows:

1. If the pod's namespace has a `PreflightConfig` with a `gangDiscovery` block, that discoverer is used.
2. Otherwise, the cluster-wide `gangDiscovery` Helm value is used as the default.

`PreflightConfig` is namespaced (`preflight.nvsentinel.nvidia.com/v1alpha1`); the object's own namespace is its scope. Its `spec.gangDiscovery` block uses the same schema as the Helm `gangDiscovery` value — an empty block selects native Kubernetes discovery. (`spec` is intentionally a container for per-namespace preflight settings, so future options can be added alongside `gangDiscovery`.)

```yaml
# team-a uses Volcano; every other namespace uses the cluster-wide default.
apiVersion: preflight.nvsentinel.nvidia.com/v1alpha1
kind: PreflightConfig
metadata:
  name: default
  namespace: team-a
spec:
  gangDiscovery:
    name: "volcano"
    annotationKeys: ["scheduling.k8s.io/group-name"]
    podGroupGVR:
      group: "scheduling.volcano.sh"
      version: "v1beta1"
      resource: "podgroups"
    minCountExpr: "podGroup.spec.minMember"
```

At most one `PreflightConfig` should exist per namespace. If more than one is present, the **oldest** object (tie-broken by name) stays active so an existing working configuration is not disrupted; the additional objects are marked not ready as superseded. Check the resolved state via the object's status:

```console
$ kubectl -n team-a get preflightconfig
NAME      DISCOVERER   READY   AGE
default   volcano      True    10s
```

Readiness is reported via the `Ready` status condition (the `READY` column above is its status); its message explains why a config is not ready (invalid or superseded). Inspect it with `kubectl -n team-a get preflightconfig default -o yaml` under `.status.conditions`.

#### RBAC (aggregated ClusterRole)

The controller reads scheduler `PodGroup` resources cluster-wide through an **aggregated ClusterRole** (`<release>-gang-discovery`). The chart ships a built-in contributor role covering the native `scheduling.k8s.io` resources plus the default `gangDiscovery.podGroupGVR`. To let the controller read a scheduler CRD that isn't covered (e.g. a namespace registers Volcano but the default is native), apply a `ClusterRole` labeled for aggregation — it is merged in automatically, with no preflight change or restart:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: preflight-gang-discovery-volcano
  labels:
    preflight.nvsentinel.nvidia.com/aggregate-to-gang-discovery: "true"
rules:
  - apiGroups: ["scheduling.volcano.sh"]
    resources: ["podgroups"]
    verbs: ["get", "list", "watch"]
```

Because `ClusterRole`s are cluster-scoped, creating one is a platform/cluster-admin action — a namespace tenant declares its scheduler via the `PreflightConfig`, while the platform grants the corresponding read access. Aggregation is eventually consistent, so a brief `Forbidden` window after adding a new contributor role is expected; the controller retries.

Each `PreflightConfig` is validated when reconciled: the `gangDiscovery.podGroupGVR` is resolved against the cluster's API RESTMapper (and native specs verify the `scheduling.k8s.io` resources). An invalid or unresolvable config does not disrupt admission — the namespace falls back to the default and the error is surfaced in the object's status.

#### Changing gang discovery configuration

`PreflightConfig` changes take effect on **newly-admitted gangs** and are applied per pod at admission. Avoid editing or deleting a namespace's active `PreflightConfig` (or deleting it so a different one becomes active) **while multi-node preflight gangs are being launched** in that namespace.

The gang ID embeds the discoverer name (`<discoverer>-<namespace>-<podGroup>`), and discovery is resolved per pod. If the effective discoverer for a namespace changes mid-flight, pods of the same gang admitted before and after the change can derive **different gang IDs** and fail to coordinate (peers never converge). Such a gang's `preflight-nccl-allreduce` check then waits until `gangCoordination.timeout` and fails — the pod stays in `Init:Error` and follows the normal NVSentinel quarantine path. This fails safe (no false "healthy" result) but causes a spurious preflight failure, so treat gang discovery config as a namespace setting to change during a quiet window. Adding a *second* `PreflightConfig` is safe — the active (oldest) one is unaffected (see above); the risk is specifically changing or removing the currently-active config.

## Gang coordination

When `gangCoordination.enabled` is true (default in the preflight chart), the controller coordinates multi-node checks through ConfigMaps:

1. At admission time the webhook creates a skeleton ConfigMap for the gang and injects it as a volume mount on the pod's preflight init containers.
2. As pods become ready the gang controller populates the ConfigMap with peer information (IP, rank).
3. Init containers read the ConfigMap at `gangCoordination.configMapMountPath` (default `/etc/preflight`) to discover the master address and peer list.

Each gang ConfigMap contains:

| Key | Value |
|-----|-------|
| `expected_count` | Minimum members needed (from the Workload / PodGroup CRD) |
| `peers` | Newline-separated list of `podName;podIP;rank` |
| `master_addr` | IP of the rank-0 pod |
| `master_port` | Port for PyTorch distributed TCP bootstrap (default `29500`) |
| `gang_id` | Unique gang identifier (discoverer prefix + namespace + group) |

ConfigMaps are labeled `nvsentinel.nvidia.com/managed-by: preflight` and named with a `preflight-` prefix.

### Key `gangCoordination` values

```yaml
gangCoordination:
  enabled: true
  timeout: "10m"            # Max wait for all members to register
  masterPort: 29500         # PyTorch distributed bootstrap port
  configMapMountPath: "/etc/preflight"

  # Azure InfiniBand topology (required for NDv4/v5)
  ncclTopoConfigMap: ""     # Pre-existing ConfigMap name, or use ncclTopoShape
  ncclTopoShape: ""         # "ndv4" or "ndv5" to auto-create from bundled XML

  extraHostPathMounts: []   # Host paths for NCCL/OFI/CUDA libraries
  extraVolumeMounts: []     # Mount existing pod volumes (e.g. GCP TCPXO plugin)
  # mirrorResourceClaims: true  # Mirror DRA claims to init containers (default true)
```

For DRA / device claims mirrored into init containers, see [ADR-026 §DRA Integration](../designs/026-preflight-checks.md) and `mirrorResourceClaims` above.

## Key Helm values (subchart)

| Area | Location |
|------|-----------|
| Webhook TLS, failure policy, cert provider | `preflight.webhook` |
| Init container placement (append/prepend) | `preflight.initContainerPlacement` |
| Injected init container images and env | `preflight.initContainers` |
| GPU / network resource names | `preflight.gpuResourceNames`, `preflight.networkResourceNames` |
| Copy NCCL / fabric env and mounts from user containers | `preflight.ncclEnvPatterns`, `preflight.volumeMountPatterns` |
| Gang discovery | `preflight.gangDiscovery` |
| Gang coordination (timeouts, topology, mounts) | `preflight.gangCoordination` |
| Namespace selector for the webhook | `preflight.namespaceSelector` |
| Pod-level selector for the webhook | `preflight.objectSelector` |

## Object selector (pod-level filtering)

By default the webhook intercepts all GPU pods in labeled namespaces. To further restrict which pods are intercepted, set `objectSelector` with standard Kubernetes label selectors. When empty (`{}`), no `objectSelector` is emitted and all pods in matching namespaces are intercepted.

Example — only intercept pods explicitly labeled for preflight:

```yaml
objectSelector:
  matchLabels:
    nvsentinel.nvidia.com/preflight: "enabled"
```

`matchExpressions` are also supported:

```yaml
objectSelector:
  matchExpressions:
    - key: nvsentinel.nvidia.com/preflight
      operator: In
      values: ["enabled", "true"]
```

This is useful when you want namespace-wide opt-in via `namespaceSelector` but only run preflight on specific workloads within those namespaces.

Full defaults and comments: `distros/kubernetes/nvsentinel/charts/preflight/values.yaml`.

Tilt development often trims init containers to DCGM-only; see `distros/kubernetes/nvsentinel/values-tilt.yaml`.

## Observability

- Webhook pod: liveness/readiness probes use `/healthz` on the webhook port.
- Prometheus metric names for check containers and the injector are specified in [ADR-026 § Metrics](../designs/026-preflight-checks.md#metrics); wire scrapers to your init container images and deployment as your environment allows.

## Debugging preflight failures

When a preflight check fails, the pod stays in `Init:Error` (or `Init:CrashLoopBackOff`), a health event enters the NVSentinel pipeline, and the node may proceed through quarantine. Use the steps below to determine whether the failure is a real hardware/interconnect problem, a misconfiguration, or an infrastructure issue.

### 1. Identify the failing check

```bash
# Pod phase and init container status
kubectl -n <namespace> describe pod <pod-name>

# Exit code and termination message for each preflight init container
kubectl -n <namespace> get pod <pod-name> -o jsonpath=\
'{range .status.initContainerStatuses[*]}{.name}{"\t"}{.state.terminated.exitCode}{"\t"}{.state.terminated.reason}{"\n"}{end}'
```

| Exit code | Meaning |
|-----------|---------|
| `0` | Check passed |
| `1` | Check failed (GPU or interconnect unhealthy) |
| `2` | Configuration error in the init container |

Focus on init containers whose names start with `preflight-`.

### 2. Read init container logs

```bash
kubectl -n <namespace> logs <pod-name> -c preflight-dcgm-diag
kubectl -n <namespace> logs <pod-name> -c preflight-nccl-loopback
kubectl -n <namespace> logs <pod-name> -c preflight-nccl-allreduce
```

For NCCL failures, temporarily raise verbosity in Helm values:

```yaml
initContainers:
  - name: preflight-nccl-allreduce
    env:
      - name: NCCL_DEBUG
        value: "INFO"
      - name: NCCL_DEBUG_SUBSYS
        value: "INIT,NET"
```

### 3. Webhook not injecting init containers

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| No `preflight-*` init containers in pod spec | Namespace not labeled | `kubectl label namespace <ns> nvsentinel.nvidia.com/preflight=enabled` |
| No injection on some pods only | `objectSelector` or `preflight-checks: ""` | Check pod labels and annotations |
| Pod creation rejected | Invalid `preflight-checks` annotation | Use comma-separated valid container names; no duplicates |
| All pod creates fail | Webhook down or TLS issue | Check preflight deployment and cert-manager certificates |

```bash
# Webhook health
kubectl -n nvsentinel get pods -l app.kubernetes.io/name=preflight
kubectl -n nvsentinel logs -l app.kubernetes.io/name=preflight --tail=100

# Recent admission failures
kubectl -n nvsentinel get events --field-selector reason=FailedMount --sort-by='.lastTimestamp'
```

If the webhook is unavailable and `webhook.failurePolicy` is `Fail`, **all** GPU pod creation is blocked — consider `Ignore` for graceful degradation during maintenance.

### 4. DCGM diagnostic failures (`preflight-dcgm-diag`)

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Connection refused / timeout to hostengine | Wrong `DCGM_HOSTENGINE_ADDR` or DCGM not running | Verify GPU Operator DCGM service; see [GPU Monitor DCGM Failures](../runbooks/gpu-monitor-dcgm-failures.md) |
| Test FAIL on memory/PCIe/NVLink | Hardware fault | Expected behavior — node quarantine proceeds |
| `DCGM_ST_*` after retries, exit 0 | DCGM infrastructure issue | Non-fatal by design; check DCGM hostengine health |
| Check too slow | `DCGM_DIAG_LEVEL` too high | Lower to `1` (~30 s) for faster startup |

```bash
kubectl -n <namespace> get pod <pod-name> -o jsonpath=\
'{range .spec.initContainers[?(@.name=="preflight-dcgm-diag")].env[?(@.name=="DCGM_HOSTENGINE_ADDR")]}{.value}{"\n"}{end}'
```

### 5. Single-node NCCL failures (`preflight-nccl-loopback`)

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Bandwidth below threshold | PCIe vs NVLink mismatch | Lower `BW_THRESHOLD_GBPS` (~15 for PCIe, ~150 for NVLink) |
| NCCL init errors | Missing GPU or driver issue | Verify `nvidia.com/gpu` resources allocated to the pod |
| Hang / timeout | GPU topology issue | Set `NCCL_DEBUG=INFO` and inspect logs |

### 6. Multi-node NCCL failures (`preflight-nccl-allreduce`)

These failures usually involve gang discovery, gang coordination, or fabric configuration.

#### Gang discovery problems

```bash
# Pod should carry the osmo.group_uuid label
kubectl -n <namespace> get pod <pod-name> -o jsonpath='{.metadata.labels.osmo\.group_uuid}{"\n"}'

# PodGroup exists and minMember matches expected gang size
kubectl -n <namespace> get podgroups.scheduling.run.ai <name> -o yaml
```

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Check skipped / no gang env vars | `gangDiscovery` not configured for your scheduler | Set OSMO + KAI or Grove config (see above) |
| `Forbidden` on PodGroup get in preflight logs | Missing RBAC | Add aggregated ClusterRole for your scheduler CRD |
| Wrong `expected_count` in ConfigMap | Incorrect `minCountExpr` | Default `podGroup.spec.minMember`; adjust for your CRD schema |
| Divergent gang IDs mid-flight | `PreflightConfig` changed during gang launch | Change gang discovery only during quiet windows |

Verify `PreflightConfig` readiness when using per-namespace overrides:

```bash
kubectl -n <namespace> get preflightconfig
kubectl -n <namespace> get preflightconfig default -o yaml
```

#### Gang coordination timeout

The init container waits up to `gangCoordination.timeout` (default `10m`) for all peers to register in the gang ConfigMap.

```bash
# Find gang ConfigMaps
kubectl -n <namespace> get configmap -l nvsentinel.nvidia.com/managed-by=preflight

# Inspect coordination state
kubectl -n <namespace> get configmap <preflight-configmap-name> -o yaml
```

| ConfigMap state | Likely cause | Fix |
|-----------------|--------------|-----|
| Missing entirely | Webhook did not detect a gang | Verify the pod carries the `osmo.group_uuid` label configured in `gangDiscovery.labelKeys` |
| `expected_count` set, `peers` empty | Pods not yet scheduled / no Pod IPs | Wait for KAI gang scheduling; check scheduler events |
| Partial `peers` list at timeout | Not all gang members admitted | Verify `minMember` matches running pod count; check gang scheduling |
| Peers present, NCCL still fails | Fabric / NCCL misconfiguration | Enable `inheritUserEnv`; set `ncclTopoShape` for Azure IB |

```bash
# Scheduler events for gang pods
kubectl -n <namespace> describe pod <pod-name> | grep -A5 Events
```

#### Fabric / NCCL configuration

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| NCCL falls back to TCP / low bandwidth | Missing topology or EFA libs | Set `gangCoordination.ncclTopoShape` or `extraHostPathMounts` |
| Works for workload but not preflight | Inheritance disabled | Set `inheritUserEnv: true` on `preflight-nccl-allreduce` |
| `preflight-checks` mismatch across gang | Inconsistent annotations | Use the same annotation on every pod in the PodGroup |

### 7. Health events and downstream effects

Preflight reports results via the Platform Connector (same path as health monitors):

```bash
# Platform connector and preflight controller logs
kubectl -n nvsentinel logs -l app.kubernetes.io/name=preflight --tail=200 | grep -iE 'gang|inject|error'

# Node conditions after a fatal preflight failure
kubectl describe node <node-name> | grep -A3 Conditions
```

A fatal GPU or interconnect failure is **expected** to trigger quarantine. Confirm the failure is genuine (check init container logs) before overriding or disabling checks.

### 8. Temporarily bypass checks (troubleshooting only)

To isolate whether preflight is causing the issue:

```yaml
# Disable all checks on a single pod:
metadata:
  annotations:
    nvsentinel.nvidia.com/preflight-checks: ""
```

Or disable preflight for the namespace by removing the label (not recommended in production):

```bash
kubectl label namespace <namespace> nvsentinel.nvidia.com/preflight-
```

### 9. Metrics

| Metric | Use |
|--------|-----|
| `preflight_check_failures_total` | Failure rate by check and error code |
| `preflight_gang_wait_seconds` | Gang coordination latency — high values indicate scheduling or discovery issues |
| `preflight_injection_total` | Webhook injection success/failure |
| `preflight_config_errors_total` | Gang discovery or configuration errors |

See [ADR-026 § Metrics](../designs/026-preflight-checks.md#metrics) for the full list.

## Related documentation

- [ADR-026: Preflight checks](../designs/026-preflight-checks.md)
- [ADR-035: Inline DCGM config](../designs/035-preflight-inline-dcgm-config.md) — design rationale for inline env var configuration
- [gRPC / TLS authentication](../designs/030-grpc-tls-authentication.md) (mentions preflight among webhooks)
- [Helm chart README](../../distros/kubernetes/README.md)
- E2E test entry point: `tests/preflight_test.go` (build tag `amd64_group`)
