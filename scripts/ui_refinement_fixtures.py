"""Literal, synthetic runtime observations for the UI regression harness."""
import copy
import time


def runtime_metrics(issues=False):
    now = int(time.time() * 1000)

    def operation():
        return {
            'active': 0, 'attempts': 12, 'succeeded': 12, 'failed': 0, 'cancelled': 0,
            'failing': False, 'consecutive_failures': 0, 'last_result': 'success',
            'last_attempt_at': now - 1000, 'last_finished_at': now - 500,
            'last_success_at': now - 500, 'last_failure_at': 0, 'recovered_at': 0,
            'last_error_code': '',
        }

    def queue():
        return {'enabled': True, 'queued': 0, 'capacity': 256, 'buffered': 0,
                'dropped': 0, 'write_failed': 0, 'written': 120, 'last_dropped_at': 0,
                'operation': operation()}

    def replication():
        return {'enabled': False, 'queued': 0, 'capacity': 0, 'dropped': 0,
                'failed': 0, 'last_dropped_at': 0, 'operation': operation()}

    components = [
        {'id': component, 'state': 'disabled' if component in ('log_replication', 'trace_replication', 'cache_sync', 'debug_cleanup', 'startup_recovery') else 'healthy',
         'reason': 'not_hybrid' if component in ('log_replication', 'trace_replication', 'cache_sync', 'startup_recovery') else 'not_enabled' if component == 'debug_cleanup' else 'ready'}
        for component in ('billing', 'request_history', 'logs', 'token_stats', 'log_replication',
                          'trace_replication', 'cache_sync', 'log_cleanup', 'debug_cleanup', 'startup_recovery', 'websocket')
    ]
    metrics = {
        'started_at': now - 600_000, 'observed_at': now,
        'health': {'version': 1, 'state': 'healthy', 'components': components},
        'billing': {'enabled': True, 'pending': 0, 'unpersisted': 0, 'blocked': False,
                    'closing': False, 'journal_failures': 0, 'store_failures': 0,
                    'invalid_cost_events': 0, 'settled': 120, 'deleted_token_events': 0,
                    'last_success_at': now - 500, 'oldest_pending_at': 0,
                    'journal': operation(), 'settlement': operation()},
        'request_traces': queue(), 'logs': queue(), 'token_failure_stats': queue(),
        'cleanup': {
            'logs': {'enabled': True, 'mode': 'retention', 'retention_days': 7,
                     'retention_minutes': 0, 'interval_ms': 3_600_000, 'next_run_at': now + 3_600_000,
                     'deleted': 100, 'operation': operation()},
            'debug_logs': {'enabled': False, 'mode': 'disabled', 'retention_days': 0,
                           'retention_minutes': 0, 'interval_ms': 120_000, 'next_run_at': 0,
                           'deleted': 0, 'operation': operation()},
        },
        'storage': {'mode': 'sqlite', 'primary': 'sqlite', 'cache_may_be_stale': False,
                    'cache_sync': operation(), 'startup_recovery': None,
                    'log_replication': replication()},
        'responses_websocket': {
            'sessions': 0, 'active_attachments': 0, 'downstream_connections': 0,
            'rejected_downstream_connections': 0, 'max_downstream_connections': 64,
            'max_downstream_connections_per_token': 16, 'upstream_connections': 0,
            'upstream_handshakes': 0, 'upstream_reuses': 0, 'upstream_heartbeat_failures': 0,
            'upstream_queued_read_bytes': 0, 'oldest_upstream_connection_seconds': 0,
            'reconnects': 0, 'transcript_bytes': 0, 'max_transcript_bytes': 134_217_728,
            'max_sessions': 32, 'ttl_expired': 0, 'capacity_rejected': 0,
            'budget_rejected': 0, 'previous_response_misses': 0,
        },
    }
    if issues:
        metrics['billing'].update(pending=8, unpersisted=2, blocked=True, invalid_cost_events=1)
        metrics['logs'].update(dropped=4, write_failed=5, last_dropped_at=now - 5000)
        metrics['storage'].update(mode='hybrid', primary='mysql', cache_may_be_stale=True)
        metrics['storage']['log_replication'].update(enabled=True, capacity=128, failed=3)
        metrics['request_traces']['replication'] = copy.deepcopy(metrics['storage']['log_replication'])
        for component in components:
            if component['id'] == 'billing':
                component.update(state='blocked', reason='billing_unpersisted')
            elif component['id'] == 'cache_sync':
                component.update(state='warning', reason='cache_needs_reconcile')
            elif component['id'] in ('log_replication', 'trace_replication'):
                component.update(state='healthy', reason='ready')
        metrics['health']['state'] = 'blocked'
    return metrics
