import { dayLabel, formatInr, groupByDay } from '../format'
import { isSplit, listPaneWidth, SPLIT_MIN_WIDTH } from '../layout'

describe('split layout breakpoint', () => {
  it('is single-pane on phones and split from 768dp', () => {
    expect(isSplit(390)).toBe(false)
    expect(isSplit(767)).toBe(false)
    expect(isSplit(SPLIT_MIN_WIDTH)).toBe(true)
    expect(isSplit(1024)).toBe(true)
  })

  it('clamps the list pane', () => {
    expect(listPaneWidth(768)).toBe(300)
    expect(listPaneWidth(1024)).toBe(341)
    expect(listPaneWidth(1600)).toBe(420)
  })
})

describe('format', () => {
  it('formats paise with Indian grouping', () => {
    expect(formatInr(0)).toBe('₹0.00')
    expect(formatInr(99900)).toBe('₹999.00')
    expect(formatInr(123456789)).toBe('₹12,34,567.89')
    expect(formatInr(-150000)).toBe('-₹1,500.00')
  })

  it('groups newest-first rows by day', () => {
    const now = new Date(2026, 10, 11, 12)
    const at = (d: number, h = 9) => new Date(2026, 10, d, h).toISOString()
    expect(dayLabel(at(11), now)).toBe('Today')
    expect(dayLabel(at(10), now)).toBe('Yesterday')
    const s = groupByDay([{ t: at(11, 10) }, { t: at(11, 8) }, { t: at(10) }, { t: at(2) }], (r) => r.t, now)
    expect(s.map((x) => [x.title, x.data.length])).toEqual([
      ['Today', 2],
      ['Yesterday', 1],
      [dayLabel(at(2), now), 1],
    ])
  })
})
