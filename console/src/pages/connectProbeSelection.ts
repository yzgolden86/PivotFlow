import type { GatewayProtocol } from './clientConnection.ts'

export interface GatewayProbeSelection {
  tokenId: number
  requestedToken: number
  token: string
  protocol: GatewayProtocol
  model: string
  generation: number
}

export interface GatewayProbeSession {
  controller: AbortController
  selection: GatewayProbeSelection
}

export function startGatewayProbe(selection: GatewayProbeSelection): GatewayProbeSession {
  return { controller: new AbortController(), selection: { ...selection } }
}

export function invalidateGatewayProbe(session: GatewayProbeSession | null): null {
  session?.controller.abort()
  return null
}

export function requestedTokenSelectionTarget(previousRequested: number, requested: number, selected: number): number | null {
  if (requested > 0) return requested === previousRequested ? null : requested
  // Removing a positive URL selection still needs the same invalidation path;
  // retain the current row while clearing its secret/result. Initial 0 stays
  // untouched so the token-loading effect can auto-select the first usable one.
  return previousRequested > 0 ? selected : null
}

export function canApplyGatewayProbe(session: GatewayProbeSession, current: GatewayProbeSelection, active: GatewayProbeSession | null): boolean {
  return active === session &&
    session.selection.tokenId === current.tokenId &&
    session.selection.requestedToken === current.requestedToken &&
    session.selection.token === current.token &&
    session.selection.protocol === current.protocol &&
    session.selection.model === current.model &&
    session.selection.generation === current.generation
}
