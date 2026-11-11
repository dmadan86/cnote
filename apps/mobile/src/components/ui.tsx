import { Ionicons } from '@expo/vector-icons'
import type { ReactNode } from 'react'
import {
  ActivityIndicator,
  Pressable,
  RefreshControl,
  type RefreshControlProps,
  StyleSheet,
  Text,
  TextInput,
  type TextInputProps,
  View,
  type ViewStyle,
} from 'react-native'
import { useSafeAreaInsets } from 'react-native-safe-area-context'
import { HIT, TAB_CLEARANCE, useTheme } from '@/lib/theme'

export function Label({
  children,
  muted,
  bold,
  size = 15,
}: {
  children: ReactNode
  muted?: boolean
  bold?: boolean
  size?: number
}) {
  const t = useTheme()
  return (
    <Text style={{ color: muted ? t.muted : t.fg, fontSize: size, fontWeight: bold ? '600' : '400' }}>
      {children}
    </Text>
  )
}

export function Button({
  title,
  onPress,
  kind = 'primary',
  disabled,
  busy,
}: {
  title: string
  onPress: () => void
  kind?: 'primary' | 'plain'
  disabled?: boolean
  busy?: boolean
}) {
  const t = useTheme()
  const primary = kind === 'primary'
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={title}
      accessibilityState={{ disabled: !!disabled || !!busy, busy: !!busy }}
      disabled={disabled || busy}
      onPress={onPress}
      style={({ pressed }) => [
        s.button,
        { backgroundColor: primary ? t.accent : t.hover, opacity: disabled ? 0.5 : pressed ? 0.8 : 1 },
      ]}
    >
      {busy ? (
        <ActivityIndicator color={primary ? t.accentFg : t.fg} />
      ) : (
        <Text style={{ color: primary ? t.accentFg : t.fg, fontSize: 15, fontWeight: '600' }}>{title}</Text>
      )}
    </Pressable>
  )
}

export function Chip({ label, active, onPress }: { label: string; active: boolean; onPress: () => void }) {
  const t = useTheme()
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={`Filter ${label}`}
      accessibilityState={{ selected: active }}
      onPress={onPress}
      style={[
        s.chip,
        { backgroundColor: active ? t.accent : t.side, borderColor: active ? t.accent : t.line },
      ]}
    >
      <Text style={{ color: active ? t.accentFg : t.fg, fontSize: 14, fontWeight: '500' }}>{label}</Text>
    </Pressable>
  )
}

type Tone = 'neutral' | 'ok' | 'warn' | 'bad' | 'accent'
export function Pill({ label, tone = 'neutral' }: { label: string; tone?: Tone }) {
  const t = useTheme()
  const c = {
    neutral: [t.hover, t.muted],
    ok: [t.okBg, t.ok],
    warn: [t.warnBg, t.warn],
    bad: [t.badBg, t.bad],
    accent: [t.hover, t.accent],
  }[tone]
  return (
    <View style={[s.pill, { backgroundColor: c[0] }]}>
      <Text style={{ color: c[1], fontSize: 12, fontWeight: '600' }}>{label}</Text>
    </View>
  )
}

/** One tappable list row: title/subtitle on the left, accessory on the right. */
export function Row({
  title,
  subtitle,
  left,
  right,
  onPress,
  selected,
  chevron,
  label,
}: {
  title: string
  subtitle?: string
  left?: ReactNode
  right?: ReactNode
  onPress?: () => void
  selected?: boolean
  chevron?: boolean
  label?: string
}) {
  const t = useTheme()
  return (
    <Pressable
      accessibilityRole={onPress ? 'button' : undefined}
      accessibilityLabel={label ?? (subtitle ? `${title}, ${subtitle}` : title)}
      accessibilityState={{ selected: !!selected }}
      disabled={!onPress}
      onPress={onPress}
      style={({ pressed }) => [
        s.row,
        { backgroundColor: selected ? t.hover : pressed ? t.hover : t.canvas, borderBottomColor: t.line },
      ]}
    >
      {left}
      <View style={{ flex: 1, gap: 2 }}>
        <Text numberOfLines={1} style={{ color: t.fg, fontSize: 16 }}>
          {title}
        </Text>
        {subtitle ? (
          <Text numberOfLines={1} style={{ color: t.muted, fontSize: 13 }}>
            {subtitle}
          </Text>
        ) : null}
      </View>
      {right}
      {chevron ? <Ionicons name="chevron-forward" size={18} color={t.muted} /> : null}
    </Pressable>
  )
}

export function SectionHeader({ title }: { title: string }) {
  const t = useTheme()
  return (
    <View style={[s.header, { backgroundColor: t.side }]} accessibilityRole="header">
      <Text style={{ color: t.muted, fontSize: 13, fontWeight: '600', textTransform: 'uppercase' }}>
        {title}
      </Text>
    </View>
  )
}

/** Loading / error / empty handling shared by every list. Renders nothing when `ok`. */
export function StateView({
  loading,
  error,
  empty,
  emptyText,
  onRetry,
}: {
  loading: boolean
  error: string | null
  empty: boolean
  emptyText: string
  onRetry: () => void
}) {
  const t = useTheme()
  if (loading)
    return <ActivityIndicator style={{ margin: 32 }} color={t.muted} accessibilityLabel="Loading" />
  if (error)
    return (
      <View style={s.state}>
        <Ionicons name="alert-circle-outline" size={32} color={t.bad} />
        <Text style={{ color: t.fg, textAlign: 'center' }} accessibilityRole="alert">
          {error}
        </Text>
        <Button title="Try again" onPress={onRetry} kind="plain" />
      </View>
    )
  if (empty)
    return (
      <View style={s.state}>
        <Ionicons name="file-tray-outline" size={32} color={t.muted} />
        <Text style={{ color: t.muted, textAlign: 'center' }}>{emptyText}</Text>
      </View>
    )
  return null
}

export function Refresh({ refreshing, onRefresh }: { refreshing: boolean; onRefresh: () => void }) {
  const t = useTheme()
  const p: RefreshControlProps = { refreshing, onRefresh, tintColor: t.muted }
  return <RefreshControl {...p} />
}

export function Field(props: TextInputProps & { label: string }) {
  const t = useTheme()
  const { label, style, ...rest } = props
  return (
    <TextInput
      accessibilityLabel={label}
      placeholder={label}
      placeholderTextColor={t.muted}
      autoCapitalize="none"
      {...rest}
      style={[s.field, { color: t.fg, backgroundColor: t.side, borderColor: t.line }, style]}
    />
  )
}

/** Floating action pill, parked above the floating tab bar. */
export function Fab({
  title,
  icon,
  onPress,
  busy,
}: {
  title: string
  icon: keyof typeof Ionicons.glyphMap
  onPress: () => void
  busy?: boolean
}) {
  const t = useTheme()
  const { bottom } = useSafeAreaInsets()
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={title}
      disabled={busy}
      onPress={onPress}
      style={[s.fab, { backgroundColor: t.accent, bottom: bottom + 80 }]}
    >
      {busy ? (
        <ActivityIndicator color={t.accentFg} />
      ) : (
        <Ionicons name={icon} size={20} color={t.accentFg} />
      )}
      <Text style={{ color: t.accentFg, fontSize: 15, fontWeight: '600' }}>{title}</Text>
    </Pressable>
  )
}

export function Screen({ children, style }: { children: ReactNode; style?: ViewStyle }) {
  const t = useTheme()
  return <View style={[{ flex: 1, backgroundColor: t.canvas }, style]}>{children}</View>
}

export const listPad = { paddingBottom: TAB_CLEARANCE + 40 }

const s = StyleSheet.create({
  button: {
    minHeight: HIT,
    borderRadius: 10,
    paddingHorizontal: 18,
    alignItems: 'center',
    justifyContent: 'center',
  },
  chip: {
    minHeight: 36,
    paddingHorizontal: 14,
    borderRadius: 18,
    borderWidth: StyleSheet.hairlineWidth,
    justifyContent: 'center',
  },
  pill: { paddingHorizontal: 8, paddingVertical: 3, borderRadius: 10 },
  row: {
    minHeight: 56,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  header: { paddingHorizontal: 16, paddingVertical: 6 },
  state: { alignItems: 'center', gap: 12, padding: 32 },
  field: {
    minHeight: HIT,
    borderRadius: 10,
    borderWidth: StyleSheet.hairlineWidth,
    paddingHorizontal: 12,
    fontSize: 16,
  },
  fab: {
    position: 'absolute',
    right: 16,
    minHeight: HIT,
    borderRadius: 24,
    paddingHorizontal: 18,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    elevation: 4,
    shadowColor: '#000',
    shadowOpacity: 0.2,
    shadowRadius: 8,
    shadowOffset: { width: 0, height: 3 },
  },
})
