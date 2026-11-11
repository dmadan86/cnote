import { Stack } from 'expo-router'
import { useTheme } from '@/lib/theme'

/** Native stack shared by the module tabs: `index` is the list, `[id]` the pushed detail (phones only). */
export function ModuleStack({ title, detailTitle }: { title: string; detailTitle: string }) {
  const t = useTheme()
  return (
    <Stack
      screenOptions={{
        headerStyle: { backgroundColor: t.canvas },
        headerTintColor: t.accent,
        headerTitleStyle: { color: t.fg },
        headerShadowVisible: false,
        contentStyle: { backgroundColor: t.canvas },
      }}
    >
      <Stack.Screen name="index" options={{ title }} />
      <Stack.Screen name="[id]" options={{ title: detailTitle }} />
    </Stack>
  )
}
