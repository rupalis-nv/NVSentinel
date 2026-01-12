# ADR-026: Change Events for NVSentinel Actions

## Context

NVSentinel performs critical operations on cluster nodes such as cordoning, uncordoning, draining, and remediation. When incidents occur, on-call engineers need visibility into what changes NVSentinel made to the cluster and when.

Currently, there is no centralized record of NVSentinel **actions**. Engineers investigating incidents must manually correlate logs, metrics, and cluster state to understand what NVSentinel did.

### What We're Sending

At its core, NVSentinel needs to emit **action events**:

```json
{
  "action": "cordon",
  "timestamp": "2025-01-12T14:30:00Z",
  "source": "nvsentinel/fault-quarantine",
  "cluster": "my-cluster-name",
  "node": "gpu-node-01",
  "healthEvent": {
    "checkName": "xid-check",
    "errorCode": "XID-79",
    "agent": "syslog-health-monitor",
    "isFatal": true
  }
}
```

### Relationship to Existing Systems

| System | What it captures | Purpose |
|--------|------------------|---------|
| **Audit Logging (ADR-016)** | Low-level HTTP API calls | Compliance, debugging |
| **Event Exporter** | Health event detections | Telemetry, analytics |
| **Change Events (this ADR)** | High-level actions + context | On-call visibility |

Change events are **not a replacement** for audit logging. They serve different purposes:
- Audit logs: "Prove what API calls were made" (compliance)
- Change events: "Tell on-call what NVSentinel did and why" (operations)

### Options Considered

1. **Direct HTTP Push** - NVSentinel sends events directly to a configurable HTTP endpoint
2. **KAaaS (Kratos Aggregates as-a-Service)** - Scheduled queries on Data Lake, push to PagerDuty
3. **Extend Audit Logging** - Add action context to existing audit logs
4. **Extend Event Exporter** - Add action events alongside health events

## Decision

**TBD** - Two viable options are under consideration:

### Option 1: Direct HTTP Push (Near Real-time)
- NVSentinel components send events directly to a configurable HTTP endpoint
- Latency: Immediate (seconds)
- Requires: New client code in NVSentinel

### Option 2: KAaaS (Near Real-time, 5-min intervals)
- Health events already flow to Data Lake via event-exporter
- Extend to include action/status data
- KAaaS scheduled query (every 5 min) pushes to PagerDuty
- Latency: Up to 5 minutes
- Requires: KAaaS onboarding, query setup

Both options can target PagerDuty or any HTTP endpoint that accepts JSON payloads.

## Implementation

### Architecture Options Analysis

Before diving into the implementation, we evaluated several architectural approaches:

#### Option A: Embedded Client in Components (Selected for Phase 1)

```text
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                    fault-quarantine pod                      ││
│  │  ┌───────────────┐     ┌─────────────────────────────────┐  ││
│  │  │  Reconciler   │────▶│  changeevent.Client             │  ││
│  │  │               │     │  - Sends HTTP POST after action │  ││
│  │  │ 1. Cordon node│     │  - Fire-and-forget              │  ││
│  │  │ 2. Send event │     │  - Non-blocking                 │  ││
│  │  └───────────────┘     └──────────────┬──────────────────┘  ││
│  └───────────────────────────────────────│──────────────────────┘│
└──────────────────────────────────────────│───────────────────────┘
                                           │ HTTPS
                                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      HTTP Sink (External)                        │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Configurable Endpoint                                      │  │
│  │  URL: {endpoint}/{clusterName}                             │  │
│  │  ┌──────────────────────────────────────────────────────┐  │  │
│  │  │ POST /changes/{clusterName}                          │  │  │
│  │  │ • Validates auth header                              │  │  │
│  │  │ • Processes JSON payload                             │  │  │
│  │  │ • Routes to downstream system                        │  │  │
│  │  └──────────────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────│────────────────────────┘
                                           │
                                           ▼
┌──────────────────────────────────────────────────────────────────┐
│               Downstream System (PagerDuty, Datadog, etc.)       │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Dashboard / Timeline View                                  │  │
│  │  ┌─────────────────────────────────────────────────────┐   │  │
│  │  │ cluster-name                                        │   │  │
│  │  │ ├── Recent Changes                                   │   │  │
│  │  │ │   ├── 14:30 NVSentinel cordoned node gpu-01       │   │  │
│  │  │ │   └── 14:45 NVSentinel uncordoned node gpu-01     │   │  │
│  │  │ └── Incidents / Alerts                               │   │  │
│  │  └─────────────────────────────────────────────────────┘   │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

**Pros:**
- Simple and direct - action and event happen together
- Real-time - no delay between action and PD event
- Easy to implement and test
- Component has full context (node, error, reason)

**Cons:**
- If pod crashes between K8s API success and PD send, event is lost
- Each component needs PD configuration
- Tight coupling between business logic and PD client

#### Option B: Extend Event-Exporter for Action Events

Instead of creating a new component, extend the existing `event-exporter` to also watch 
for action/status changes and send to PagerDuty.

```text
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
│                                                                   │
│  ┌─────────────────┐      ┌─────────────────┐                   │
│  │fault-quarantine │      │  Datastore      │                   │
│  │ 1. Cordon node  │─────▶│  (MongoDB/PG)   │                   │
│  │ 2. Update status│      │  health_events  │                   │
│  └─────────────────┘      └────────┬────────┘                   │
│                                    │ Change Stream               │
│                                    ▼                             │
│                    ┌───────────────────────────────┐            │
│                    │      event-exporter           │            │
│                    │  (extended, not new)          │            │
│                    │                               │            │
│                    │  ┌─────────────────────────┐  │            │
│                    │  │ Sink 1: Telemetry       │──┼──▶ Data Lake│
│                    │  │ (existing)              │  │            │
│                    │  └─────────────────────────┘  │            │
│                    │                               │            │
│                    │  ┌─────────────────────────┐  │            │
│                    │  │ Sink 2: Change Events   │──┼──▶ HTTP Sink│
│                    │  │ (new sink, filters for  │  │            │
│                    │  │  status changes only)   │  │            │
│                    │  └─────────────────────────┘  │            │
│                    └───────────────────────────────┘            │
└─────────────────────────────────────────────────────────────────┘
```

**Implementation options:**
1. **Same collection, filter for status changes** - Watch health_events, emit to PD only when status changes (quarantined, draining, etc.)
2. **Separate action events table** - Create new table for action events, event-exporter watches both
3. **Multi-sink support** - Add ability to route events to multiple sinks based on event type

**Pros:**
- Decoupled - components don't need PD knowledge
- Reliable - events persisted in datastore before sending
- **No new component** - extends existing event-exporter
- Can replay missed events
- Consistent architecture with existing telemetry flow

**Cons:**
- Slight delay (milliseconds to seconds)
- Event-exporter becomes more complex
- State transitions may not capture full context (depends on what's in datastore)
- Need to ensure status/action data is stored in datastore

#### Option C: Via Platform-Connector

```text
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
│                                                                   │
│  ┌─────────────────┐      ┌─────────────────────────────────┐   │
│  │ Health Monitors │─────▶│    platform-connector           │   │
│  └─────────────────┘      │    ├── K8s Connector            │   │
│                           │    └── PD Connector (new)       │───▶ PD
│  ┌─────────────────┐      │                                  │   │
│  │fault-quarantine │─────▶│    Receives state transitions   │   │
│  └─────────────────┘      └─────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

**Pros:**
- Centralized - single integration point
- Platform-connector already handles events
- No new component

**Cons:**
- Couples platform-connector to PagerDuty
- Platform-connector sees health events, not actions (cordon is separate)
- Would need protocol changes

#### Option D: KAaaS (Kratos Aggregates as-a-Service)

```text
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
│                                                                   │
│  ┌─────────────────┐      ┌─────────────────┐                   │
│  │fault-quarantine │      │  Datastore      │                   │
│  │ 1. Cordon node  │─────▶│  (MongoDB/PG)   │                   │
│  │ 2. Update status│      │  status field   │                   │
│  └─────────────────┘      └────────┬────────┘                   │
│                                    │                             │
│  ┌─────────────────┐               │ (already exists)           │
│  │ event-exporter  │───────────────┼───────────────────────────▶│
│  └─────────────────┘               │                             │
└────────────────────────────────────│─────────────────────────────┘
                                     │
                                     ▼
┌────────────────────────────────────────────────────────────────┐
│                    Kratos Data Lake                             │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  nvsentinel.telemetry.health-events.prod                  │  │
│  │  + action/status data (if extended)                       │  │
│  └────────────────────────────┬─────────────────────────────┘  │
└───────────────────────────────│─────────────────────────────────┘
                                │
                                ▼ Scheduled Query (every 5 min)
┌───────────────────────────────────────────────────────────────┐
│                         KAaaS                                  │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │  SQL Query:                                              │  │
│  │  SELECT node, action, timestamp, check_name, error_code  │  │
│  │  FROM health_events                                      │  │
│  │  WHERE updated_at > NOW() - INTERVAL 5 MINUTES           │  │
│  │    AND status IN ('quarantined', 'draining', ...)        │  │
│  └─────────────────────────────────────────────────────────┘  │
│                         │                                      │
│                         ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │  Push to PagerDuty (built-in integration)               │  │
│  └─────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────┘
```

**Pros:**
- Fully managed infrastructure (99.95% SLA)
- No new code in NVSentinel (if data already in Data Lake)
- Built-in PagerDuty integration
- Aggregation, filtering, alerting capabilities
- Out-of-the-box monitoring dashboard

**Cons:**
- 5-minute minimum latency (not real-time)
- Requires KAaaS onboarding
- Need to ensure action/status data is in Data Lake
- External dependency on Kratos infrastructure

**Gap Analysis:**
- Event-exporter currently sends **health events** (detections)
- Does NOT include **actions** (cordon, uncordon, drain status)
- Would need to extend event-exporter OR query datastore directly

### Approach Comparison

| Aspect | Option A (Direct Push) | Option D (KAaaS) |
|--------|------------------------|------------------|
| **Latency** | Immediate (seconds) | Up to 5 minutes |
| **Code changes** | New changeevent package | Minimal/none |
| **Infrastructure** | Self-managed | Fully managed (99.95% SLA) |
| **Reliability** | Fire-and-forget | Guaranteed delivery |
| **Setup** | Helm values | KAaaS onboarding |
| **Context** | Full (in component) | Depends on data in Lake |

### Recommended Approach: TBD

**If real-time is required** → Option A (Direct Push)
**If near real-time (5 min) is acceptable** → Option D (KAaaS) may be simpler


### High-Level Architecture

```text
┌────────────────────────────────────────────────────────────────────────┐
│                         NVSentinel Cluster                              │
│                                                                         │
│  ┌─────────────┐   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐│
│  │   fault-    │   │    node-    │   │   fault-    │   │   janitor   ││
│  │ quarantine  │   │   drainer   │   │ remediation │   │             ││
│  │  (Phase 1)  │   │  (Phase 2)  │   │  (Phase 2)  │   │  (Phase 2)  ││
│  └──────┬──────┘   └──────┬──────┘   └──────┬──────┘   └──────┬──────┘│
│         │                 │                 │                 │        │
│         └────────────────┬┴─────────────────┴─────────────────┘        │
│                          ▼                                              │
│         ┌────────────────────────────────────────────┐                 │
│         │        commons/pkg/changeevent/            │                 │
│         │  ┌──────────────────────────────────────┐  │                 │
│         │  │ • HTTP client for external sink      │  │                 │
│         │  │ • Builds change event payload        │  │                 │
│         │  │ • Async send (goroutine)             │  │                 │
│         │  │ • Metrics & logging                  │  │                 │
│         │  └──────────────────────────────────────┘  │                 │
│         └────────────────────┬───────────────────────┘                 │
│                              │                                          │
│  ┌───────────────────────────┼──────────────────────────────────────┐  │
│  │ Configuration             │                                       │  │
│  │ ┌───────────────────┐     │     ┌──────────────────────────┐     │  │
│  │ │ Helm Values       │     │     │ K8s Secret               │     │  │
│  │ │ - endpoint        │     │     │ change-events-token      │     │  │
│  │ │ - clusterName     │     │     │ - token: <auth-token>    │     │  │
│  │ │ - enabled         │     │     └──────────────────────────┘     │  │
│  │ └───────────────────┘     │                                       │  │
│  └───────────────────────────┼──────────────────────────────────────┘  │
└──────────────────────────────│──────────────────────────────────────────┘
                               │ HTTPS POST
                               │ /{clusterName}
                               ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                        HTTP Sink (External)                              │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │  Processing                                                         │  │
│  │  1. Validate auth header                                            │  │
│  │  2. Extract cluster name from URL path                              │  │
│  │  3. Process JSON payload                                            │  │
│  │  4. Route to downstream system (PagerDuty, Datadog, etc.)          │  │
│  └────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                       Downstream Dashboard                               │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │  Cluster View: {clusterName}                                        │  │
│  │  ┌──────────────────────────────────────────────────────────────┐  │  │
│  │  │  Recent Changes                                               │  │  │
│  │  │  ┌────────────────────────────────────────────────────────┐  │  │  │
│  │  │  │ 14:30 NVSentinel cordoned node gpu-01 due to XID-79    │  │  │  │
│  │  │  │ 14:35 NVSentinel drained node gpu-01                   │  │  │  │
│  │  │  │ 14:40 NVSentinel initiated reboot for gpu-01           │  │  │  │
│  │  │  │ 15:10 NVSentinel uncordoned node gpu-01                │  │  │  │
│  │  │  └────────────────────────────────────────────────────────┘  │  │  │
│  │  └──────────────────────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────┘
```

### Data Flow (Phase 1: Cordon Event)

```text
1. Health event detected (GPU error)
              │
              ▼
2. fault-quarantine reconciler receives event
              │
              ▼
3. Reconciler evaluates rules → decides to cordon
              │
              ▼
4. Call K8s API: QuarantineNodeAndSetAnnotations()
              │
              ├──[FAIL]──▶ Log error, return (no PD event)
              │
              ▼ [SUCCESS]
5. Build ChangeEvent and call changeEventClient.SendChangeEvent()
   (returns immediately - non-blocking)
              │
              ▼
6. Reconciler continues (operation complete)

   ┌─────────────────────────────────────────────┐
   │ Inside changeevent.Client (async goroutine):│
   │                                             │
   │  POST to configured HTTP sink               │
   │         │                                   │
   │         ├──[FAIL]──▶ Log error              │
   │         │            Increment failure metric│
   │         │                                   │
   │         ▼ [SUCCESS]                         │
   │  Log success, increment success metric      │
   └─────────────────────────────────────────────┘
```

### Phase 1: Fault Quarantine Events

| Action | Change Event Summary |
|--------|---------------------|
| Node cordoned | "NVSentinel cordoned node {nodeName} due to {errorCode}" |
| Node uncordoned | "NVSentinel uncordoned node {nodeName} - all health checks passed" |

### API

NVSentinel sends change events to a **configurable HTTP endpoint**. The endpoint, 
authentication, and payload format are all configurable.

**Request**:
```text
POST {endpoint}/{clusterName}
Content-Type: application/json
{authHeader}: {authToken}
```

**Payload Schema** (Action + Health Event Details):
```json
{
  "action": "cordon",
  "timestamp": "2025-08-26T14:42:58.315+0000",
  "source": "nvsentinel/fault-quarantine",
  "cluster": "my-cluster-name",
  "node": "gpu-node-01",
  "healthEvent": {
    "checkName": "xid-check",
    "errorCode": "XID-79",
    "agent": "syslog-health-monitor",
    "isFatal": true,
    "message": "GPU error detected"
  },
  "dryRun": false
}
```

> **Note**: The payload schema is NVSentinel's internal format. If the target endpoint 
> requires a different format (e.g., PagerDuty Change Events API), the client can 
> transform the payload accordingly.

**Expected Response Codes**:
- `2xx`: Success
- `401/403`: Authentication failure
- `404`: Unknown cluster/resource

### New Package: `commons/pkg/changeevent/`

```text
commons/pkg/changeevent/
├── client.go          # HTTP client for change event sink
├── client_test.go     # Unit tests
├── types.go           # ChangeEvent, Payload structs
└── config.go          # Configuration types
```

**Client Interface**:
```go
// Client defines the interface for sending change events to an external sink.
// All methods are safe for concurrent use.
//
// Lifecycle:
//   1. Create client with NewClient(cfg)
//   2. Call SendChangeEvent() from any goroutine (safe for concurrent use)
//   3. Call Close() during shutdown to wait for pending sends
//   4. After Close() returns, do not call SendChangeEvent()
type Client interface {
    // SendChangeEvent sends a change event to the configured HTTP endpoint.
    //
    // This method is NON-BLOCKING and FIRE-AND-FORGET. It spawns an internal
    // goroutine to send the HTTP request and returns immediately. Callers should
    // NOT wrap this in a goroutine.
    //
    // Context handling:
    //   - The caller's context is used ONLY for immediate validation/enqueueing
    //   - The spawned goroutine uses a DETACHED context with Config.Timeout
    //   - Caller context cancellation does NOT cancel the HTTP request
    //   - This ensures the event is sent even if the caller's context is cancelled
    //
    // There is no error return because:
    //   - The actual HTTP send happens asynchronously in a goroutine
    //   - Errors are logged internally and exposed via Prometheus metrics
    //   - Sink failures should never block or fail cluster operations
    //
    // No-op conditions (returns immediately, no goroutine spawned):
    //   - Client is nil (nil-receiver safe)
    //   - Client is disabled (Config.Enabled=false)
    //   - Client is closed (Close() has been called)
    SendChangeEvent(ctx context.Context, event ChangeEvent)

    // Close shuts down the client and waits for pending sends to complete.
    //
    // Behavior:
    //   - Marks client as closed (subsequent SendChangeEvent calls become no-ops)
    //   - Waits up to Config.ShutdownTimeout for in-flight HTTP requests to finish
    //   - Returns nil if all pending sends complete within the timeout
    //   - Returns error if timeout elapses before all pending sends finish
    //
    // Callers MUST NOT call SendChangeEvent after Close returns.
    // Close is idempotent; calling it multiple times is safe.
    Close() error
}

type Config struct {
    // Enabled controls whether SendChangeEvent actually sends events.
    // If false, SendChangeEvent is a no-op (returns immediately, no goroutine).
    Enabled bool

    // Endpoint is the HTTP sink URL (e.g., https://events.example.com/changes).
    // Required if Enabled is true.
    Endpoint string

    // ClusterName is appended to Endpoint as a path segment.
    // Required if Enabled is true.
    ClusterName string

    // AuthHeader is the HTTP header name for authentication (e.g., "Authorization").
    AuthHeader string

    // AuthToken is the value for the auth header (from K8s Secret).
    AuthToken string

    // Timeout is the HTTP request timeout for each send operation.
    // This timeout is enforced in the spawned goroutine using a detached context.
    // The caller's context deadline is NOT used for the HTTP request.
    // Default: 10s if not set.
    Timeout time.Duration

    // ShutdownTimeout is the maximum time Close() waits for pending sends.
    // If pending sends don't complete within this duration, Close() returns an error.
    // Default: 10s if not set.
    ShutdownTimeout time.Duration
}

// NewClient creates a new change event client.
// Returns an error if Enabled is true but required fields (Endpoint, ClusterName) are missing.
// Applies default values for Timeout (10s) and ShutdownTimeout (10s) if not set.
// If Config.Enabled is false, returns a no-op client that ignores all SendChangeEvent calls.
func NewClient(cfg Config) (Client, error)
```

### Integration Points

**fault-quarantine/pkg/reconciler/reconciler.go**:
```go
// After successful cordon
err := r.k8sClient.QuarantineNodeAndSetAnnotations(...)
if err == nil {
    // SendChangeEvent is fire-and-forget (no error return).
    // Spawns internal goroutine, returns immediately.
    // Errors logged via slog and exposed via Prometheus metrics.
    r.changeEventClient.SendChangeEvent(ctx, changeevent.ChangeEvent{
        Action:    "cordon",
        NodeName:  event.HealthEvent.NodeName,
        CheckName: event.HealthEvent.CheckName,
        ErrorCode: event.HealthEvent.ErrorCode,
        Agent:     event.HealthEvent.Agent,
        Timestamp: time.Now().UTC(),
    })
}

// After successful uncordon
err := r.k8sClient.UnQuarantineNodeAndRemoveAnnotations(...)
if err == nil {
    // Fire-and-forget: no error to check, safe to call inline.
    r.changeEventClient.SendChangeEvent(ctx, changeevent.ChangeEvent{
        Action:    "uncordon",
        NodeName:  event.NodeName,
        CheckName: event.CheckName,
        Reason:    "all health checks passed",
        Timestamp: time.Now().UTC(),
    })
}
```

**Design Rationale**: `SendChangeEvent` has no error return because:
1. **Fire-and-forget semantics**: The actual HTTP send happens asynchronously
2. **No caller error handling needed**: Errors are logged and exposed via metrics
3. **Clean call sites**: Callers don't need to ignore errors or wrap in goroutines
4. **Guaranteed non-blocking**: PagerDuty issues can never impact cluster operations

### Configuration (Helm Values)

**values.yaml**:
```yaml
global:
  changeEvents:
    enabled: false  # Opt-in
    endpoint: ""    # HTTP sink URL (required if enabled)
    clusterName: "" # Cluster identifier (required if enabled)
    timeout: "10s"
    auth:
      headerName: "Authorization"  # Auth header name
      secretRef:
        name: "change-events-token"
        key: "token"
```

**values.yaml.tmpl** (nvsentinel-oss):
```yaml
global:
  changeEvents:
    enabled: true
    endpoint: {{ .Config.changeEventsEndpoint }}
    clusterName: {{ .This.metadata.name }}
```

### Error Handling

1. **Non-blocking**: Sink failures should NOT block NVSentinel operations
2. **Fire-and-forget**: Send in goroutine, log errors, don't retry
3. **Metrics**: Add Prometheus metrics for observability:
   - `nvsentinel_change_events_total{action, status}`
   - `nvsentinel_change_events_failed_total{action, error}`

### Dry Run Behavior

When `global.dryRun: true`:
- Still send change events to sink
- Include `"dryRun": true` in payload
- Summary prefix: "[DRY-RUN] NVSentinel would have cordoned..."

## Rationale

1. **Simple HTTP interface**: NVSentinel only needs an HTTP client with configurable endpoint
2. **Decoupled from sink**: Any HTTP endpoint that accepts JSON payloads can receive events
3. **Consistent pattern**: Same approach used for existing event-exporter
4. **Minimal complexity**: No vendor-specific SDKs or protocols required

## Consequences

### Positive

- Visibility into NVSentinel actions for operations teams
- Better incident correlation - what changed before the incident?
- Audit trail in external system
- Extensible to other components (node-drainer, fault-remediation, janitor)
- Sink-agnostic: can route to PagerDuty, Datadog, custom dashboards, etc.

### Negative

- Dependency on external HTTP sink availability
- Token/credential management overhead
- Additional network calls per operation

### Mitigations

- Fire-and-forget pattern ensures NVSentinel operations are not blocked
- Configurable auth supports various credential rotation mechanisms
- Metrics and logging for observability of failures

## Alternatives Considered

### Alternative 1: Change Stream Watcher (Option B)

A dedicated component that watches the datastore for state changes and emits PD events.

**Pros:**
- Decoupled architecture
- Events persisted before sending (reliable)
- Can replay missed events

**Deferred** because:
- More complex to implement
- New component to deploy/maintain
- May lose context (why the action happened)
- Can revisit for Phase 2 if reliability becomes a concern

### Alternative 2: Via Platform-Connector (Option C)

Add PD change event emission to the existing platform-connector.

**Rejected** because:
- Platform-connector handles health events, not actions (cordon/drain)
- Would require protocol changes to pass action context
- Couples unrelated concerns

### Alternative 3: Direct Vendor API Integration

Call vendor APIs (e.g., PagerDuty, Datadog) directly from NVSentinel.

**Rejected** because:
- Would require vendor-specific code for each integration target
- NVSentinel would need to manage vendor credentials/routing per cluster
- Better to have a configurable HTTP sink that can be fronted by any proxy/router

### Alternative 4: Extend Audit Logging 

Extend the existing audit logging mechanism to include health event details
and add the ability to push to external HTTP endpoints.

**Current audit logging:**
- Captures low-level HTTP API calls via RoundTripper
- Writes to file (`/var/log/nvsentinel/audit.log`)
- Format: `{timestamp, component, method, url, requestBody}`

**What would need to change:**
1. Add health event context (checkName, errorCode, reason) to audit entries
2. Add real-time HTTP push capability (currently file-only)
3. Transform audit entries to change event format

**Pros:**
- Reuses existing infrastructure
- Single audit trail for both compliance and operations
- No new packages needed

**Cons:**
- Mixes two different concerns (compliance audit vs operational visibility)
- Audit logs are at HTTP level, change events are at action level
- Would need significant refactoring of RoundTripper to inject health event context
- File-based audit logs are for compliance; real-time push is for operations

**Deferred** because:
- Audit logging and change events serve different purposes
- Extending audit logging would conflate compliance and operational concerns
- However, this could be revisited if we want a unified event system in the future

**Relationship clarified:**
| Aspect | Audit Logging | Change Events |
|--------|---------------|---------------|
| Purpose | Compliance, debugging | On-call visibility |
| Level | HTTP API calls | Semantic actions |
| Destination | File (shipped by operators) | Real-time HTTP push |
| Consumer | Security/compliance teams | On-call engineers |
| Context | Request body | Action + health event reason |

### Alternative 5: KAaaS (Kratos Aggregates as-a-Service)

Use KAaaS to query the Data Lake for NVSentinel events and push to PagerDuty on a schedule.

**How it would work:**
1. Event-exporter already sends health events to Data Lake
2. Extend to include action/status data (or query datastore)
3. KAaaS runs scheduled SQL query (every 5 minutes)
4. Pushes results to PagerDuty via built-in integration

**Pros:**
- Fully managed infrastructure (99.95% SLA)
- Minimal/no code changes if data already in Data Lake
- Built-in PagerDuty integration
- Aggregation and alerting capabilities

**Cons:**
- 5-minute minimum latency (not real-time)
- Requires KAaaS onboarding
- Need to ensure action/status data flows to Data Lake

**Status:** Under consideration as Option D in main design.
See [KAaaS Documentation](https://docs.kratos.nvidia.com/query/datalake/kaaas.html)

### Alternative 6: Centralized Event Bus

Introduce Kafka/NATS for internal event distribution with PD as a consumer.

**Rejected** because:
- Over-engineering for current requirements
- Introduces significant infrastructure overhead
- No other consumers currently need this
- Can be considered in future if event distribution needs grow

## Phase 2 (Future)

Extend to other components after Phase 1 validation:

| Component | Events |
|-----------|--------|
| node-drainer | Drain started, Drain completed, Drain failed |
| fault-remediation | RebootNode CR created, TerminateNode CR created |
| janitor | Reboot executed, Terminate executed |

## Open Questions

1. **Auth token provisioning**: How will the auth token be provided to NVSentinel clusters?
   - Option A: K8s Secret synced from secret manager
   - Option B: Part of cluster bootstrapping (Terraform)

2. **Links field**: Should we include any URLs in the change event payload?
   - Grafana dashboard link?
   - Runbook link?

3. **Sink endpoint**: Which HTTP sink will be used in production?
   - Depends on downstream system requirements

## References

- [JIRA: HIPPO-2399](https://jirasw.nvidia.com/browse/HIPPO-2399)
- [ADR-016: Audit Logging](./016-audit-logging.md) - Similar pattern for file-based audit logs
- [PagerDuty Change Events API](https://developer.pagerduty.com/docs/events-api-v2/send-change-events/) - Example downstream format

