import { useColorScheme } from 'react-native'

// Same tokens as apps/web/src/app/globals.css so both clients read as one product.
const light = {
  canvas: '#ffffff',
  side: '#f7f7f8',
  hover: '#ececef',
  line: '#e4e4e7',
  fg: '#18181b',
  muted: '#6b6b76',
  accent: '#4f46e5',
  accentFg: '#ffffff',
  ok: '#15803d',
  okBg: '#dcfce7',
  warn: '#a16207',
  warnBg: '#fef3c7',
  bad: '#b91c1c',
  badBg: '#fee2e2',
}
const dark: typeof light = {
  canvas: '#111113',
  side: '#18181b',
  hover: '#26262b',
  line: '#2e2e35',
  fg: '#ececef',
  muted: '#9a9aa6',
  accent: '#818cf8',
  accentFg: '#0b0b0d',
  ok: '#86efac',
  okBg: '#14301f',
  warn: '#fcd34d',
  warnBg: '#33290b',
  bad: '#fca5a5',
  badBg: '#3a1414',
}

export type Theme = typeof light
export const useTheme = (): Theme => (useColorScheme() === 'dark' ? dark : light)

/** Minimum touch target (Apple HIG / Material). */
export const HIT = 44
/** Bottom padding that keeps content clear of the floating tab bar. */
export const TAB_CLEARANCE = 96
