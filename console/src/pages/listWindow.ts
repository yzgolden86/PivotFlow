export function listWindow(total: number, page: number, pageSize: number) {
  const count = Number.isFinite(total) ? Math.max(0, Math.floor(total)) : 0
  const size = Number.isFinite(pageSize) && pageSize >= 1 ? Math.floor(pageSize) : 50
  const pages = Math.max(1, Math.ceil(count / size))
  const current = Number.isFinite(page) ? Math.min(pages, Math.max(1, Math.floor(page))) : 1
  const start = (current - 1) * size
  return { page: current, pages, pageSize: size, total: count, start, end: Math.min(count, start + size) }
}

export function parsePageInput(value: string, pages: number): number | null {
  const input = value.trim()
  if (!/^\d+$/.test(input)) return null
  const page = Number(input)
  return Number.isSafeInteger(page) && page >= 1 && page <= pages ? page : null
}
