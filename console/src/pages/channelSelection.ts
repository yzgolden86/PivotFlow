export function applyPageSelection(
  current: ReadonlySet<number>,
  pageIDs: readonly number[],
  selected: boolean,
): Set<number> {
  const next = new Set(current)
  for (const id of pageIDs) {
    if (selected) next.add(id)
    else next.delete(id)
  }
  return next
}

export function withoutSelectedIDs(current: ReadonlySet<number>, removedIDs: readonly number[]): Set<number> {
  const removed = new Set(removedIDs)
  return new Set([...current].filter((id) => !removed.has(id)))
}

export function countSelected(current: ReadonlySet<number>, matchingIDs: readonly number[]): number {
  return matchingIDs.reduce((count, id) => count + Number(current.has(id)), 0)
}
