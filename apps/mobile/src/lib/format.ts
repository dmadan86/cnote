/** Money is i64 paise on the wire; show rupees with Indian digit grouping (12,34,567.00). */
export function formatInr(paise: number): string {
  const neg = paise < 0
  const abs = Math.abs(Math.trunc(paise))
  const rupees = Math.floor(abs / 100)
  const frac = String(abs % 100).padStart(2, '0')
  const s = String(rupees)
  const head = s.slice(0, -3)
  const grouped = (head ? `${head.replace(/\B(?=(\d{2})+(?!\d))/g, ',')},` : '') + s.slice(-3)
  return `${neg ? '-' : ''}₹${grouped}.${frac}`
}

const dayKey = (d: Date) => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`

/** "Today", "Yesterday", else "Mon, 3 Nov" (plus the year when it is not the current one). */
export function dayLabel(iso: string, now: Date = new Date()): string {
  const d = new Date(iso)
  if (dayKey(d) === dayKey(now)) return 'Today'
  const y = new Date(now)
  y.setDate(y.getDate() - 1)
  if (dayKey(d) === dayKey(y)) return 'Yesterday'
  return d.toLocaleDateString('en-IN', {
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric',
  })
}

/** Group rows (already newest first) into consecutive day sections for a SectionList. */
export function groupByDay<T>(rows: T[], when: (r: T) => string, now?: Date) {
  const sections: { title: string; data: T[] }[] = []
  for (const r of rows) {
    const title = dayLabel(when(r), now)
    const last = sections[sections.length - 1]
    if (last?.title === title) last.data.push(r)
    else sections.push({ title, data: [r] })
  }
  return sections
}
