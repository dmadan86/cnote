import { describe, expect, it } from 'vitest'
import { groupOf } from '../components/docs/task-groups'

const t = (due: string | null) => ({
  note_id: 'n',
  note_title: '',
  icon: null,
  space_id: 's',
  ord: 0,
  text: 'x',
  due,
  done: false,
})

describe('My tasks groups', () => {
  it('split by due date around today', () => {
    const today = '2026-11-30'
    expect(groupOf(t('2026-11-29'), today)).toBe('Overdue')
    expect(groupOf(t('2026-11-30'), today)).toBe('Today')
    expect(groupOf(t('2026-12-06'), today)).toBe('This week') // across a month end
    expect(groupOf(t('2026-12-07'), today)).toBe('Later')
    expect(groupOf(t(null), today)).toBe('No date')
  })
})
