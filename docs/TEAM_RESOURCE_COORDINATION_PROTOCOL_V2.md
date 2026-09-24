# Tether Team Resource Coordination Protocol

_Status: Design specification draft_  
_Last updated: 2026-09-15_

This document defines the resource-coordination protocol for **Tether Team**. It is intentionally narrower and more formal than the broader Team product plan. Its purpose is to specify how shared Agents are allocated, leased, fenced, monitored, degraded, reconciled, and safely recovered across normal operation, Team Server outages, Orchestrator failures, Agent failures, partial partitions, lease expiry, reconnect, stale-controller recovery, and long-running workloads.

This protocol should be treated as a **high-risk distributed coordination component**. Implementation should not begin until the state machine, invariants, timing assumptions, and failure semantics are reviewed.

---

## 1. Scope

This protocol governs shared resource coordination in Tether Team.

Under the fixed-membership model, Agents are not dynamically shared between Orchestrators. Each Agent belongs to one Orchestrator/cluster at a time. The Team Server schedules work between clusters; the Orchestrator schedules work across its own Agents.

It defines:

- ownership and allocation
- lease expiry and renewal
- stale-controller fencing
- workload execution bounds
- controller liveness
- degraded operation
- quarantine
- reconnect and reconciliation
- persistence requirements
- timing assumptions

It does not define billing, Personal licensing, Enterprise networking, llama.cpp internals, UI implementation details, or Team user authentication.

---

## 2. Roles

### Team Server

The Team Server is the **allocation authority**.

It is the only component allowed to issue a new ownership generation for a shared Agent.

Responsibilities:

- allocate Agents
- renew ownership leases
- issue monotonically increasing generations
- record expected ownership
- coordinate shared scheduling
- reconcile after disconnects
- refuse unsafe ownership changes while protected work exists

### Orchestrator

The Orchestrator is the **execution authority for resources it currently owns**.

Responsibilities:

- request leases
- start workloads while admission is permitted
- maintain controller heartbeats
- report workload completion
- respect admission deadlines
- cache current lease state
- report state after reconnect

It must not create or extend ownership locally.

### Agent

The Agent is the **local enforcement authority**.

Responsibilities:

- enforce ownership
- enforce generations
- enforce admission deadlines
- enforce execution deadlines
- track controller liveness
- reject stale controllers
- refuse conflicting ownership transitions
- preserve valid protected workloads
- clean up orphaned workloads
- quarantine ambiguous state

The Agent is not a scheduler.

---

## 3. Authority Model

```text
allocation authority
= Team Server

execution authority
= Orchestrator, while authorized

local enforcement authority
= Agent
```

The Team Server decides who owns a resource. The Orchestrator uses that resource. The Agent enforces the ownership decision even if the Team Server becomes unreachable.

---

## 3A. Fixed Cluster Membership

An Agent belongs to **exactly one Orchestrator/cluster at a time**.

Normal Team scheduling happens at the cluster level:

```text
Team Server
    ↓
select Cluster B
    ↓
Orchestrator B
    ↓
Cluster B's Agents
```

The Team Server does **not** dynamically borrow or move individual Agents between clusters in response to VRAM demand.

Example:

```text
Cluster A
Orchestrator A
├── Agent A1
└── Agent A2

Cluster B
Orchestrator B
├── Agent B1
└── Agent B2
```

If Cluster B needs additional VRAM, Agent A2 remains part of Cluster A unless an explicit reconfiguration operation moves it.

This fixed-membership rule is a core Team invariant because it prevents ordinary request scheduling from becoming cross-Orchestrator GPU ownership arbitration.

### Normal scheduling

Normal scheduling means:

```text
request
    ↓
Team Server selects a Cluster
    ↓
selected Orchestrator decides model placement
    ↓
that Orchestrator uses only its bound Agents
```

The Team Server decides **which cluster receives the request**.

The Orchestrator decides **how the requested model is placed across Agents inside that cluster**.

### Agent rebinding

Moving an Agent between clusters is allowed only as an explicit administrative reconfiguration operation.

Example:

```text
Move Agent A2
from Cluster A
to Cluster B
```

A safe rebind should conceptually perform:

```text
1. Verify Agent A2 is idle
2. Verify Cluster A has no active workload using it
3. Release Agent A2 from Orchestrator A
4. Invalidate the old cluster binding
5. Bind/pair Agent A2 to Orchestrator B
6. Verify Orchestrator B can control it
7. Update Team topology
```

Rebinding is therefore:

- explicit
- infrequent
- stateful
- administrative

It is **not** part of per-request scheduling and is not triggered automatically because another cluster needs more VRAM.

A future automatic rebalance feature would require a separate protocol and should not be assumed by this specification.

---

## 4. Safety Invariants

### Invariant 0 — Fixed Agent membership

At any given time:

```text
Agent -> exactly one Orchestrator/cluster
```

Normal request scheduling may choose among clusters, but may not change Agent membership.

Agent movement requires an explicit rebind/reconfiguration operation.


### Invariant 1 — Single accepted owner generation

An Agent accepts resource-control commands from at most one active ownership generation.

```text
accepted_generation = G
command_generation < G
=> reject
```

### Invariant 2 — Only Team Server creates ownership generations

```text
new ownership
=> requires Team Server grant
```

### Invariant 3 — Fencing is monotonic

```text
generation 42 -> Orchestrator A
generation 43 -> Orchestrator B
```

Once generation 43 is accepted, generation 42 may never regain authority.

### Invariant 4 — Admission ends at the lease boundary

```text
now >= admit_until
=> no new workloads
```

### Invariant 5 — Running workloads are bounded

Every workload receives a finite execution deadline.

```text
now >= execution_deadline
=> workload must stop
```

### Invariant 6 — Dead controllers cannot retain execution indefinitely

A controller declared dead causes its managed workload to enter stop/cleanup.

### Invariant 7 — Team Server unreachability does not erase a valid lease

```text
Team Server unreachable
!= lease invalid
```

### Invariant 8 — Lease extension is server-only

Heartbeats, local activity, and starting new work never extend `admit_until`.

### Invariant 9 — Reconciliation / Team Server reconnect alone must not kill protected work

A reconciliation event, Team Server reconnect, or metadata mismatch does not by itself terminate a workload that still has a valid generation, a live controller, and an unexpired execution deadline.

This invariant is scoped specifically to reconciliation and Team Server reconnect behavior. It does **not** prevent termination triggered by other already-authorized mechanisms such as:

- execution deadline expiry (Invariant 5)
- controller death / heartbeat timeout (Invariant 6)
- future explicit administrative revoke policy

### Invariant 10 — Ambiguous ownership fails closed

```text
ownership cannot be proven
=> QUARANTINED
```

---

## 5. Lease Model

A lease represents ownership and permission to admit new work.

Minimum fields:

```text
resource_id
lease_id
owner_orchestrator_id
generation
issued_at
admit_until
```

Example:

```text
resource_id: agent-x
lease_id: lease-abc123
owner_orchestrator_id: orch-a
generation: 42
issued_at: 18:00
admit_until: 18:30
```

A lease does **not** permit a workload to run forever.

---

## 6. Workload Grant Model

When an Orchestrator starts work under a valid lease, the Agent records a workload execution grant.

Minimum fields:

```text
workload_id
resource_id
owner_orchestrator_id
generation
started_at
execution_deadline
```

Example:

```text
workload_id: job-991
resource_id: agent-x
owner_orchestrator_id: orch-a
generation: 42
started_at: 18:20
execution_deadline: 19:20
```

The execution deadline is independent of lease renewal.

---

## 7. Lease vs. Workload Semantics

The lease answers:

> Who owns this Agent, and until when may they admit new work?

The workload grant answers:

> How long may this already-admitted workload continue?

Example:

```text
Lease admit_until: 18:30
Workload started: 18:20
Execution deadline: 19:20
```

At 18:31:

```text
new workload
=> rejected
```

The existing workload may continue until normal completion or 19:20.

This prevents indefinite priority-squatting by starting one long workload shortly before lease expiry.

---

## 8. Lease Renewal

During normal operation, the Team Server should renew leases before expiry.

```text
18:00 lease -> admit_until 18:30
18:10 renew -> admit_until 18:40
18:20 renew -> admit_until 18:50
```

Renewal:

- does not change ownership generation
- extends the admission window
- must originate from the Team Server
- must be accepted by the Agent

An ownership change requires a new generation.

---

## 9. Resource States

Primary Agent resource states:

```text
UNASSIGNED
LEASED_IDLE
RUNNING
EXPIRED_RUNNING
STOPPING
QUARANTINED
```

Team Server reachability is an independent environmental condition, not a resource state.

---

## 10. Independent Inputs

### Lease

```text
NONE
VALID
EXPIRED
```

### Controller liveness

```text
ALIVE
SUSPECT
DEAD
```

### Workload

```text
IDLE
RUNNING
CLEANING_UP
```

### Team Server reachability

```text
REACHABLE
UNREACHABLE
```

### Generation

```text
accepted_generation = integer
```

### Execution deadline

```text
NOT_REACHED
REACHED
```

This matters because a lease can be valid while the controller is dead, or expired while the controller remains alive.

---

## 11. State Definitions

### UNASSIGNED

- no active ownership
- no workload
- ownership is clear and non-ambiguous
- Agent is healthy and idle
- may accept a new Team Server grant
- no Orchestrator may self-assign

`UNASSIGNED` is the normal idle/no-owner state after ordinary lease expiry or completed cleanup. It is not an error state.

### LEASED_IDLE

- valid lease exists
- no workload is running
- owner may start work while `now < admit_until`
- owner heartbeat expected

### RUNNING

- workload active under accepted generation
- owner commands allowed
- heartbeat required
- additional admission only while lease remains valid

### EXPIRED_RUNNING

- admission window expired
- already-authorized workload still active
- no new workload may start
- no local lease extension
- no ownership change may preempt the protected workload
- workload remains bounded by its execution deadline

### STOPPING

The Agent is actively terminating or cleaning up a workload.

`STOPPING` records a `stop_reason`, for example:

```text
EXECUTION_DEADLINE
CONTROLLER_DEAD
ADMIN_REVOKE
WORKLOAD_ERROR
```

Operational behavior depends on the reason:

- `EXECUTION_DEADLINE` with a live controller: request graceful stop first, then escalate to forced termination if `graceful_stop_timeout` is exceeded.
- `CONTROLLER_DEAD`: begin forced cleanup immediately because no live controller is available to coordinate shutdown.

While `STOPPING`:

- no new workload may start
- no new owner may take over
- cleanup must reach a deterministic terminal state

### QUARANTINED

- ownership or recovery state is ambiguous, inconsistent, or unsafe
- Agent refuses self-allocation
- Team Server reconciliation or explicit intervention is required

`QUARANTINED` is reserved for genuinely unsafe/ambiguous states. Ordinary lease expiry with clear ownership history should result in `UNASSIGNED`, not `QUARANTINED`.

---

## 12. Unified Transition Table

This table is the canonical state-transition artifact for the protocol. New behavior should be reflected here rather than existing only in prose.

| Current state | Trigger | Condition | Next state | Required action |
|---|---|---|---|---|
| `UNASSIGNED` | Lease grant | generation > accepted generation | `LEASED_IDLE` | Accept owner + generation |
| `LEASED_IDLE` | Start workload | lease valid + correct owner | `RUNNING` | Create workload grant |
| `LEASED_IDLE` | Lease expiry | no workload | `UNASSIGNED` | Clear ordinary lease ownership |
| `LEASED_IDLE` | Heartbeat timeout | owner dead | `UNASSIGNED` | Revoke local controller use; no workload to clean |
| `RUNNING` | Workload completes | lease valid | `LEASED_IDLE` | Cleanup workload |
| `RUNNING` | Workload completes | lease expired | `UNASSIGNED` | Cleanup workload; ownership is clear |
| `RUNNING` | Lease expiry | controller alive | `EXPIRED_RUNNING` | Block new workloads |
| `RUNNING` | Heartbeat timeout | any lease state | `STOPPING(reason=CONTROLLER_DEAD)` | Begin forced cleanup |
| `RUNNING` | Execution deadline reached | controller alive | `STOPPING(reason=EXECUTION_DEADLINE)` | Request graceful stop |
| `RUNNING` | Execution deadline reached | controller dead/suspect beyond DEAD threshold | `STOPPING(reason=CONTROLLER_DEAD)` | Begin forced cleanup |
| `EXPIRED_RUNNING` | Workload completes | — | `UNASSIGNED` | Cleanup workload; clear expired ownership |
| `EXPIRED_RUNNING` | Heartbeat timeout | — | `STOPPING(reason=CONTROLLER_DEAD)` | Begin forced cleanup |
| `EXPIRED_RUNNING` | Execution deadline reached | controller alive | `STOPPING(reason=EXECUTION_DEADLINE)` | Request graceful stop |
| `EXPIRED_RUNNING` | Execution deadline reached | controller dead/suspect beyond DEAD threshold | `STOPPING(reason=CONTROLLER_DEAD)` | Begin forced cleanup |
| `STOPPING(reason=EXECUTION_DEADLINE)` | Orchestrator acknowledges and workload stops before timeout | controller alive | `UNASSIGNED` | Graceful cleanup complete |
| `STOPPING(reason=EXECUTION_DEADLINE)` | `graceful_stop_timeout` exceeded | workload still running | `STOPPING(reason=EXECUTION_DEADLINE, forced=true)` | Escalate to forced termination |
| `STOPPING(reason=EXECUTION_DEADLINE, forced=true)` | Forced cleanup complete | — | `UNASSIGNED` | Cleanup complete |
| `STOPPING(reason=CONTROLLER_DEAD)` | Forced cleanup complete | — | `UNASSIGNED` | Cleanup complete |
| `UNASSIGNED` | Conflicting or unverifiable ownership evidence appears during recovery | — | `QUARANTINED` | Fail closed |
| `QUARANTINED` | Authoritative recovery / lease grant | Team Server proves safe state + newer generation if ownership changes | `LEASED_IDLE` or `UNASSIGNED` | Clear ambiguity and restore canonical state |


## 13. Heartbeat Model

The Agent independently detects Orchestrator liveness. The Team Server is not part of this liveness path.

Example values:

```text
heartbeat_interval = 5s
suspect_after = 15s
dead_after = 30s
```

These are examples, not final constants.

Heartbeat calibration is a **correctness-and-UX parameter**, not mere tuning. A false `DEAD` verdict revokes local controller authority and may require a Team Server round-trip before that Orchestrator can safely regain control. `SUSPECT` should absorb ordinary packet loss and transient Wi-Fi instability; `DEAD` should mean the system is prepared to revoke the controller.

```text
ALIVE
  |
  | missed heartbeats
  v
SUSPECT
  |
  | dead_after exceeded
  v
DEAD
```

`SUSPECT` does not immediately terminate a workload. `DEAD` triggers stop/cleanup behavior.

If heartbeats resume **after** the Agent has already committed to `DEAD` handling, the old controller does not automatically regain authority. Reacquisition must go through the Team Server, preserving the rule that ownership cannot be recreated locally.

---

## 14. Heartbeat Loss Before Lease Expiry

```text
state = RUNNING
lease = VALID
controller = DEAD
```

Result:

```text
RUNNING -> STOPPING(reason=CONTROLLER_DEAD)
```

The Agent does not wait for lease expiry.

Authorization and liveness are independent facts.

---

## 15. Lease Expiry While Controller Is Alive

```text
state = RUNNING
lease = EXPIRED
controller = ALIVE
```

Result:

```text
RUNNING -> EXPIRED_RUNNING
```

The active workload remains protected until:

- normal completion
- controller death
- execution deadline

No new workload may start.

---

## 16. Stop / Cleanup Semantics

```text
heartbeat timeout
    ->
controller DEAD
    ->
managed workload orphaned
    ->
Agent terminates/stops workload
    ->
cleanup completes
    ->
QUARANTINED
```

The exact subprocess cleanup behavior is implementation-specific but must be deterministic.

---

## 17. Execution Deadline

Every workload must have a finite maximum runtime.

Example:

```text
max_workload_runtime = 60 minutes
```

Then:

```text
execution_deadline = started_at + max_workload_runtime
```

Possible future policy:

```text
default: 60 minutes
organization configurable: up to 4 hours
enterprise: policy-defined
```

The essential requirement is that no workload gains unbounded ownership simply because it started before lease expiry.

---

## 18. Long-Running Workload Behavior

Example:

```text
lease expires: 18:30
job starts: 18:20
job execution deadline: 19:20
```

At 18:32, the Team Server reconnects.

The workload is still protected. The Team Server must not reassign the Agent merely because admission expired.

The Agent reports:

```text
generation: 42
workload: running
execution_deadline: 19:20
```

The resource remains non-transferable until:

- workload completion
- controller death
- execution deadline

This is bounded, not indefinite.

---

## 19. New Work During Team Server Outage

If Team Server is unreachable:

### Lease still valid

The current owner may start new work while:

```text
now < admit_until
```

### Lease expired

New work is forbidden.

Starting new work never extends the lease.

---

## 20. Team Server Outage Semantics

Team Server failure alone does not force a resource-state transition.

Example:

```text
state = RUNNING
Team Server = UNREACHABLE
lease = VALID
controller = ALIVE
```

Result:

```text
continue RUNNING
```

Allowed:

- existing workload continues
- owner commands continue
- new work may start before `admit_until`

Not allowed:

- lease renewal
- new ownership
- ownership transfer
- new global scheduling decisions

---

## 21. Partial Partition Semantics

Example:

```text
Orchestrator A ----X---- Team Server ---- Orchestrator B
        |
      Agent X
```

A can still reach Agent X. B can still reach Team Server.

The Team Server must not assume loss of contact with A means Agent X is free.

Ownership transfer requires confirmation that the Agent is transferable.

The Agent remains the local enforcement authority.

---

## 22. Transferability Rule

An Agent is transferable only if:

```text
no protected workload
cleanup complete
no unresolved ownership ambiguity
new generation > accepted generation
```

The Team Server must not reassign an Agent while it is:

```text
RUNNING
EXPIRED_RUNNING
STOPPING
```

unless a future explicit administrative override policy is defined.

---

## 23. Fencing Semantics

A higher generation fences stale controllers, but does not automatically preempt a legitimate protected workload.

Example:

```text
Agent:
generation 42
owner A
protected workload active
```

If Team Server attempts:

```text
generation 43
owner B
```

Agent should reject or defer with something like:

```text
RESOURCE_BUSY_PROTECTED
current_generation = 42
```

The server waits until the resource becomes transferable.

Fencing prevents stale control. It does not imply unconditional preemption.

---

## 24. Ownership Transfer

```text
1. Agent becomes transferable
2. Team Server increments generation
3. Team Server grants G+1 to new owner
4. Agent accepts G+1
5. Agent permanently rejects <= G
```

Example:

```text
A owns generation 42
resource becomes transferable
Team Server grants B generation 43
Agent accepts 43
A later sends generation 42 command
=> rejected
```

---

## 25. Unassigned vs Quarantined Semantics

### `UNASSIGNED`

`UNASSIGNED` is the normal, non-error state for an idle Agent with no active owner.

Typical causes:

- ordinary lease expiry with no active workload
- workload completion after lease expiry
- cleanup completion after controller death
- cleanup completion after execution deadline

Properties:

- Agent is healthy
- Agent is idle
- ownership history is clear
- no Orchestrator may self-assign
- Team Server may safely issue a new lease

### `QUARANTINED`

`QUARANTINED` is reserved for genuine ambiguity or protocol inconsistency.

Typical causes:

- conflicting ownership claims
- unverifiable generation history
- incomplete/unsafe recovery after restart
- invalid ownership transition
- reconciliation cannot establish a safe canonical state

Properties:

- Agent refuses new work
- Team Server reconciliation or explicit intervention is required
- ownership must not be guessed

This distinction prevents ordinary lease expiry from being treated as a severe fault condition.

---

## 26. STRICT Degraded Policy

Initial Team recommendation:

```text
STRICT
```

Meaning:

```text
expired idle resource
-> UNASSIGNED

no Team Server
-> no new allocation
```

Advantages:

- stronger split-brain protection
- easier reasoning
- simpler implementation
- clearer correctness model

Disadvantage:

- long Team Server outages may leave healthy idle GPUs unavailable

A future resilient degraded-allocation mode is deliberately deferred.

---

## 27. Reconciliation Goals

Reconnect reconciliation must:

1. preserve valid running workloads
2. reject stale controllers
3. restore Team Server authority
4. avoid duplicate ownership
5. quarantine ambiguity
6. never use simplistic last-write-wins for live ownership

---

## 28. Reconciliation Inputs

From Agent:

```text
resource_id
accepted_generation
current_owner
lease_id
admission state
workload_id
workload state
execution_deadline
controller liveness
```

From Orchestrator:

```text
cached lease
cached generation
known workload
last successful coordination state
```

From Team Server database:

```text
expected owner
last issued generation
last known lease
last known workload metadata
```

---

## 29. Reconciliation Authority

For runtime truth:

> Agent-reported runtime state plus accepted fencing generation outranks stale Orchestrator claims.

Example:

```text
Server expected:
A / generation 42

Agent reports:
A / generation 42 / workload running

B claims:
Agent X belongs to B
```

Resolution:

```text
preserve A
reject B claim
```

---

## 30. Reconciliation Algorithm

```text
1. Team Server reconnects to Agent
2. Read Agent runtime state
3. Read accepted generation
4. Compare with stored generation
5. Orchestrators report cached state
6. Classify resource:
   - consistent
   - stale claimant
   - protected running
   - stop/cleanup
   - ambiguous
7. Adopt safe live state where valid
8. Invalidate stale claims
9. Keep ambiguous resources quarantined
10. Issue new ownership only when transferable
```

---

## 31. Preserve Protected Work

If Agent reports:

```text
RUNNING
or
EXPIRED_RUNNING
```

and:

```text
controller alive
execution deadline not reached
generation valid
```

then reconnect does not terminate the workload.

The Team Server updates its metadata to reflect live state.

---

## 32. Reject Stale Controllers

If:

```text
controller generation < Agent accepted_generation
```

then:

```text
reject controller
invalidate stale cached lease
```

No voting is required.

---

## 33. Ambiguity Fails Closed

If safe ownership history cannot be established:

```text
=> QUARANTINED
```

Do not invent an owner from timestamps, reconnect order, database row order, or hostname ordering.

---

## 34. Crash / Restart Persistence

The Agent must persist enough state to preserve fencing safety across restart.

At minimum:

```text
highest_accepted_generation
current ownership metadata if active
current workload metadata if recoverable
```

Otherwise an Agent restart could forget generation 43 and incorrectly accept stale generation 42.

This persistence requirement is mandatory.

---

## 35. Team Server Persistence

Persist:

```text
last issued generation per resource
current expected owner
lease metadata
resource state metadata
```

Generation counters must survive Team Server restart.

A restart must never reset a resource generation to a lower value.

---

## 36. Orchestrator Persistence

Persist enough cached state for reconnect reporting:

```text
current lease
current generation
current workload IDs
```

This state is informative, not authoritative over Agent generation.

---

## 37. Clock Model

Time is a correctness concern because the protocol uses:

```text
admit_until
execution_deadline
heartbeat timeouts
```

Preferred initial rule:

> Use local monotonic timers for durations after a lease or workload is accepted, rather than relying on synchronized wall clocks for correctness.

Example:

Team Server sends:

```text
lease_duration = 30m
max_workload_runtime = 60m
```

Agent records local monotonic deadlines.

Wall-clock timestamps may still be stored for observability.

---

## 38. Lease Timing Recommendation

Prefer transmitting:

```text
lease_duration
```

or:

```text
issued_at
duration
```

Agent computes:

```text
local_admit_deadline = local_monotonic_now + duration
```

Only an authorized Team Server renewal may extend that deadline.

---

## 39. Execution Timing Recommendation

When a workload starts:

```text
local_execution_deadline =
local_monotonic_now + max_workload_runtime
```

Agent restart semantics must define how remaining time is reconstructed.

That remains an implementation detail requiring a persistence design.

---

## 40. Security Binding

Lease grants should be cryptographically bound to:

```text
resource_id
owner_orchestrator_id
generation
lease_id
duration / expiry policy
```

An Orchestrator must not be able to edit owner, generation, or lease duration without invalidating authorization.

The exact signed-envelope format is not yet defined.

---

## 41. Command Validation

Every Team Agent control command should carry enough context to validate:

```text
orchestrator identity
resource_id
lease_id
generation
```

Conceptual validation order:

```text
1. authenticate Orchestrator
2. verify resource
3. verify generation
4. verify lease ownership
5. verify command is allowed in current state
6. execute
```

---

## 42. Workload Start Validation

A workload start is accepted only if:

```text
owner == authenticated Orchestrator
generation == accepted generation
lease valid
now < local admission deadline
controller != DEAD
state permits admission
```

Otherwise it is rejected.

---

## 43. Workload Completion

Orchestrator reports:

```text
workload completed
```

Agent verifies owner, generation, and workload ID.

Then:

```text
RUNNING + valid lease
-> LEASED_IDLE
```

or:

```text
EXPIRED_RUNNING
-> QUARANTINED
```

---

## 44. Administrative Override

No administrative force-preemption behavior is defined in this draft.

If later added, it must be explicit and separate from ordinary reconciliation.

Possible future operation:

```text
FORCE_REVOKE
```

Until specified:

> Healthy protected workloads are not preempted by ordinary reconciliation.

---

## 45. Required Failure Scenarios

The protocol must be tested against at least:

### A — Team Server total outage

Expected:

- existing workload continues
- valid owner keeps control
- no new ownership
- lease eventually expires
- already-running work may continue within execution deadline

### B — Lease expires during running workload

```text
RUNNING -> EXPIRED_RUNNING
```

### C — Orchestrator crashes before lease expiry

```text
heartbeat timeout
RUNNING -> STOPPING(reason=CONTROLLER_DEAD)
```

### D — Orchestrator crashes after lease expiry

```text
EXPIRED_RUNNING -> STOPPING(reason=CONTROLLER_DEAD)
```

### E — Team Server reconnects during protected workload

Expected:

- workload preserved
- live state adopted
- no transfer until resource is transferable

### F — Stale Orchestrator reconnects

```text
Agent generation 43
Orchestrator generation 42
=> reject
```

### G — Agent restarts

Expected:

- highest accepted generation survives
- stale generations remain rejected

### H — Team Server restarts

Expected:

- generation counters survive
- no rollback
- reconciliation occurs before unsafe reassignment

### I — Partial partition

Expected:

- server does not blindly reassign
- Agent keeps enforcing protected state
- later ownership requires transferability + higher generation

### J — Long workload near lease expiry

Expected:

- admitted before expiry
- no new work after expiry
- workload bounded by execution deadline
- no unbounded squatting

---

## 46. Protocol Testing Requirements

Failure-injection tests should cover:

- Team Server process kill
- Agent process kill
- Orchestrator process kill
- network partition
- delayed messages
- reordered messages
- duplicate messages
- stale lease replay
- stale generation replay
- lease renewal race
- workload completion vs lease-expiry race
- heartbeat timeout vs workload-completion race
- reconnect during cleanup
- Team Server restart with stale database snapshot
- Agent restart with persisted generation
- wall-clock jump
- long-running workload boundary

---

## 47. Correctness Properties

### Safety

```text
Never two accepted active owners for the same Agent generation history.
```

### Fencing

```text
A lower generation never regains authority.
```

### Bounded execution

```text
No workload runs beyond its configured execution ceiling.
```

### Liveness

A resource eventually becomes reusable after owner death, workload completion, execution deadline, and Team Server recovery, subject to STRICT quarantine semantics.

### Fail-closed ambiguity

```text
unknown ownership
!= implicit ownership
```

---

## 48. Risk Classification

This protocol should be considered:

> **Critical / highest-complexity Team component**

Failure can produce:

- split-brain ownership
- duplicate workload execution
- stale controller access
- unexpected workload termination
- unavailable Agents
- resource starvation
- incorrect lease transfer
- unsafe recovery after restart

This coordination layer is likely more complex than the basic LLM orchestration itself.

---

## 49. Implementation Gate

Before coding the Team coordination layer, finalize:

- resource state machine
- transition table
- lease schema
- workload grant schema
- generation persistence
- Agent restart semantics
- Team Server restart semantics
- heartbeat thresholds
- execution runtime ceiling
- monotonic timing model
- signed lease/grant representation
- cleanup policy
- reconciliation classification rules
- failure-injection test matrix

---

## 50. Open Questions

Heartbeat thresholds are **high-priority protocol parameters** because false `DEAD` detection directly affects perceived reliability and can revoke a healthy controller during ordinary network instability.


- exact default lease duration
- exact renewal interval
- heartbeat interval
- `SUSPECT` timeout
- `DEAD` timeout
- default maximum workload runtime
- whether runtime limit is configurable
- whether some workloads may request longer bounded grants
- Agent restart behavior for active workloads
- persistence format for local lease state
- signed lease-envelope format
- direct Team Server polling vs Orchestrator relays during reconciliation
- quarantine-release UX
- future resilient degraded-allocation mode
- administrative force-preemption
- Team Server HA
- if HA exists, consensus/leader-election mechanism
- exact Agent rebind protocol and rollback semantics

---

## 51. Summary

> **The Team Server allocates.**

> **The Orchestrator executes.**

> **The Agent enforces.**

> **Leases bound admission.**

> **Workload grants bound execution.**

> **Generations fence stale controllers.**

> **Heartbeats detect dead controllers.**

> **UNASSIGNED handles ordinary no-owner idle state; QUARANTINED is reserved for ambiguity.**

> **Reconnect preserves legitimate running work but does not restore stale authority.**

> **No resource may be held forever merely because a workload started before lease expiry.**


---

## 52. Shared Model Weights vs Conversation Context

A loaded model can serve multiple users or conversations at the same time, but the conversations must **not** share one context.

The model weights are shared:

```text
Cluster C2
50B model weights loaded once
```

Each active conversation/session has its own independent context state:

```text
50B model weights
    |
    +-- Session A -> context / KV cache A
    +-- Session B -> context / KV cache B
    +-- Session C -> context / KV cache C
```

The model therefore does not "remember all conversations together."

Each request/session must remain isolated from the others.

### Scheduler implication

The Team Server must not treat:

```text
model loaded
```

as equivalent to:

```text
cluster has capacity
```

A cluster may already have the requested model loaded but still be saturated because of:

- active inference slots
- KV-cache usage
- configured context size
- concurrent sequence limits
- available VRAM headroom

The Team Server should therefore track, at minimum:

```text
loaded model
active sessions
available inference slots
KV-cache usage / capacity
available VRAM headroom
```

### Routing preference

For a new request, the Team Server should generally prefer:

```text
1. Cluster already running the requested model and with free session/context capacity
2. Another compatible cluster that can load the model
3. Queue on an appropriate cluster if no compatible cluster currently has capacity
```

Example:

```text
Cluster C2
50B loaded
4/4 inference slots occupied

Cluster C4
50B fits
currently idle
```

A new 50B conversation should be routed to C4 rather than forced into C2.

If no other cluster can host the model, the request should queue or fail according to configured queue policy.

### Core rule

> **Model weights may be shared across sessions; conversation context must remain isolated per session.**

This distinction is required for correct multi-user Team scheduling.
