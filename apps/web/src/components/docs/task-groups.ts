import type { MyTask } from '@/lib/docs'

export const GROUPS = ['Overdue', 'Today', 'This week', 'Later', 'No date'] as const
export type Group = (typeof GROUPS)[number]

const addDays = (iso: string, n: number) => {
  const d = new Date(`${iso}T00:00:00`)
  d.setDate(d.getDate() + n)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

export function groupOf(t: MyTask, today: string): Group {
  if (!t.due) return 'No date'
  if (t.due < today) return 'Overdue'
  if (t.due === today) return 'Today'
  return t.due <= addDays(today, 6) ? 'This week' : 'Later'
}
