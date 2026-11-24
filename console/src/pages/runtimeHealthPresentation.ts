import type {
  RuntimeComponentId, RuntimeHealthReason, RuntimeHealthState, RuntimeMetrics,
} from '../types.ts'

export type RuntimeComponentGroup = 'primary' | 'routine' | 'maintenance' | 'optional'
export type RuntimeAttentionSeverity = 'critical' | 'warning' | 'unknown'
export type RuntimeAttentionKind =
  | 'stale_snapshot' | 'component_fault' | 'component_warning' | 'component_unknown'
  | 'unpersisted' | 'invalid_cost' | 'write_loss'
  | 'replication_loss' | 'replica_difference' | 'recovery_failure'

export interface RuntimeAttentionItem {
  id: string
  componentId?: RuntimeComponentId
  severity: RuntimeAttentionSeverity
  kinds: RuntimeAttentionKind[]
  reason?: RuntimeHealthReason
}

export interface RuntimeComponentGroups {
  primary: RuntimeComponentId[]
  routine: RuntimeComponentId[]
  maintenance: RuntimeComponentId[]
  optional: RuntimeComponentId[]
}

const primaryComponents = new Set<RuntimeComponentId>(['billing', 'request_history'])
const maintenanceComponents = new Set<RuntimeComponentId>(['log_cleanup', 'debug_cleanup', 'startup_recovery'])
const severityOrder: Record<RuntimeAttentionSeverity, number> = { critical: 0, warning: 1, unknown: 2 }

export function runtimeOverviewState(items: RuntimeAttentionItem[], state: RuntimeHealthState): RuntimeHealthState {
  if (items.some((item) => item.kinds.includes('stale_snapshot'))) return 'unknown'
  if (state === 'blocked') return 'blocked'
  if (items.some((item) => item.severity === 'critical')) return 'error'
  if (items.some((item) => item.severity === 'warning')) return 'warning'
  if (items.some((item) => item.severity === 'unknown')) return 'unknown'
  return state
}

export function runtimeComponentGroup(state: RuntimeHealthState, id: RuntimeComponentId): RuntimeComponentGroup {
  if (primaryComponents.has(id)) return 'primary'
  if (state === 'disabled') return 'optional'
  if (maintenanceComponents.has(id)) return 'maintenance'
  return 'routine'
}

export function groupRuntimeComponents(snapshot: RuntimeMetrics): RuntimeComponentGroups {
  const groups: RuntimeComponentGroups = { primary: [], routine: [], maintenance: [], optional: [] }
  for (const component of snapshot.health.components) {
    groups[runtimeComponentGroup(component.state, component.id)].push(component.id)
  }
  return groups
}

function severityForState(state: RuntimeHealthState): RuntimeAttentionSeverity | null {
  if (state === 'blocked' || state === 'error') return 'critical'
  if (state === 'warning') return 'warning'
  if (state === 'unknown') return 'unknown'
  return null
}

function kindForState(state: RuntimeHealthState): RuntimeAttentionKind | null {
  if (state === 'blocked' || state === 'error') return 'component_fault'
  if (state === 'warning') return 'component_warning'
  if (state === 'unknown') return 'component_unknown'
  return null
}

function higherSeverity(current: RuntimeAttentionSeverity, next: RuntimeAttentionSeverity): RuntimeAttentionSeverity {
  return severityOrder[next] < severityOrder[current] ? next : current
}

export function runtimeAttentionItems(snapshot: RuntimeMetrics, freshness: 'current' | 'paused' | 'stale' | 'unavailable'): RuntimeAttentionItem[] {
  const items = new Map<string, RuntimeAttentionItem>()
  const add = (
    componentId: RuntimeComponentId | undefined,
    severity: RuntimeAttentionSeverity,
    kind: RuntimeAttentionKind,
    reason?: RuntimeHealthReason,
  ) => {
    const id = componentId ? `component:${componentId}` : 'snapshot:stale'
    const current = items.get(id)
    if (current) {
      current.severity = higherSeverity(current.severity, severity)
      if (!current.kinds.includes(kind)) current.kinds.push(kind)
      if (!current.reason && reason) current.reason = reason
      return
    }
    items.set(id, { id, componentId, severity, kinds: [kind], reason })
  }

  if (freshness === 'stale' || freshness === 'unavailable') {
    add(undefined, 'unknown', 'stale_snapshot')
  } else {
    for (const component of snapshot.health.components) {
      const severity = severityForState(component.state)
      const kind = kindForState(component.state)
      if (severity && kind) add(component.id, severity, kind, component.reason)
    }
  }

  if (snapshot.billing.unpersisted > 0) add('billing', 'critical', 'unpersisted')
  if (snapshot.billing.invalid_cost_events > 0) add('billing', 'warning', 'invalid_cost')

  const queues: Array<[RuntimeComponentId, typeof snapshot.request_traces]> = [
    ['request_history', snapshot.request_traces],
    ['logs', snapshot.logs],
    ['token_stats', snapshot.token_failure_stats],
  ]
  for (const [id, stats] of queues) {
    if (stats.write_failed > 0 || stats.dropped > 0) add(id, 'warning', 'write_loss')
  }

  if (snapshot.storage.log_replication.failed > 0 || snapshot.storage.log_replication.dropped > 0) {
    add('log_replication', 'warning', 'replication_loss')
  }
  if (snapshot.request_traces.replication
    && (snapshot.request_traces.replication.failed > 0 || snapshot.request_traces.replication.dropped > 0)) {
    add('trace_replication', 'warning', 'replication_loss')
  }
  if (snapshot.storage.cache_may_be_stale) add('cache_sync', 'warning', 'replica_difference')

  if (snapshot.storage.startup_recovery) {
    const recovery = snapshot.storage.startup_recovery
    if (recovery.configurations.failed + recovery.logs.failed + recovery.request_traces.failed > 0) {
      add('startup_recovery', 'warning', 'recovery_failure')
    }
  }

  return [...items.values()]
}
