import assert from 'node:assert/strict'
import test from 'node:test'
import { canApplyGatewayProbe, invalidateGatewayProbe, requestedTokenSelectionTarget, startGatewayProbe, type GatewayProbeSelection } from './connectProbeSelection.ts'

const selection: GatewayProbeSelection = {
  tokenId: 17,
  requestedToken: 17,
  token: 'pf-synthetic-downstream',
  protocol: 'codex',
  model: 'gpt-5',
  generation: 4,
}

test('a probe result belongs only to the exact launched selection', () => {
  const session = startGatewayProbe(selection)
  assert.equal(canApplyGatewayProbe(session, selection, session), true)

  for (const [field, value] of [
    ['tokenId', 18],
    ['requestedToken', 18],
    ['token', 'pf-other-downstream'],
    ['protocol', 'anthropic'],
    ['model', 'other-model'],
    ['generation', 5],
  ] as const) {
    assert.equal(canApplyGatewayProbe(session, { ...selection, [field]: value }, session), false, field)
  }
})

test('token switching invalidates the active probe while a user stop keeps it for the unknown result', () => {
  const first = startGatewayProbe(selection)
  assert.equal(invalidateGatewayProbe(first), null)
  assert.equal(first.controller.signal.aborted, true)

  const nextSelection: GatewayProbeSelection = {
    ...selection,
    tokenId: 18,
    requestedToken: 18,
    token: 'pf-synthetic-next',
    generation: 5,
  }
  const next = startGatewayProbe(nextSelection)
  assert.equal(canApplyGatewayProbe(first, nextSelection, next), false)
  assert.equal(canApplyGatewayProbe(next, nextSelection, next), true)

  const stoppedByUser = startGatewayProbe(selection)
  stoppedByUser.controller.abort()
  assert.equal(stoppedByUser.controller.signal.aborted, true)
  assert.equal(canApplyGatewayProbe(stoppedByUser, selection, stoppedByUser), true)
})

test('URL token removal invalidates the current selection but the initial empty parameter does not auto-select', () => {
  assert.equal(requestedTokenSelectionTarget(0, 0, 0), null)
  assert.equal(requestedTokenSelectionTarget(0, 18, 0), 18)
  assert.equal(requestedTokenSelectionTarget(18, 0, 18), 18)
  assert.equal(requestedTokenSelectionTarget(18, 0, 22), 22)
  assert.equal(requestedTokenSelectionTarget(18, 19, 18), 19)
  assert.equal(requestedTokenSelectionTarget(18, 18, 18), null)
})
