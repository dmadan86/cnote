import { Ionicons } from '@expo/vector-icons'
import { Tabs } from 'expo-router'
import { useWindowDimensions } from 'react-native'
import { useSafeAreaInsets } from 'react-native-safe-area-context'
import { useTheme } from '@/lib/theme'

type Icon = keyof typeof Ionicons.glyphMap
const TABS: { name: string; title: string; icon: Icon }[] = [
  { name: 'index', title: 'Home', icon: 'home-outline' },
  { name: 'pages', title: 'Pages', icon: 'document-text-outline' },
  { name: 'projects', title: 'Projects', icon: 'albums-outline' },
  { name: 'crm', title: 'CRM', icon: 'people-outline' },
  { name: 'billing', title: 'Billing', icon: 'receipt-outline' },
]

/** Floating, rounded tab bar (GitHub iOS style); on tablets it is centred at a fixed width. */
export default function TabsLayout() {
  const t = useTheme()
  const { bottom } = useSafeAreaInsets()
  const { width } = useWindowDimensions()
  const side = Math.max(16, (width - 560) / 2)
  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: t.accent,
        tabBarInactiveTintColor: t.muted,
        tabBarStyle: {
          position: 'absolute',
          left: side,
          right: side,
          bottom: bottom + 8,
          height: 64,
          borderRadius: 32,
          paddingBottom: 8,
          paddingTop: 8,
          backgroundColor: t.side,
          borderTopWidth: 0,
          borderWidth: 1,
          borderColor: t.line,
          elevation: 8,
          shadowColor: '#000',
          shadowOpacity: 0.15,
          shadowRadius: 12,
          shadowOffset: { width: 0, height: 4 },
        },
        tabBarLabelStyle: { fontSize: 11, fontWeight: '600' },
      }}
    >
      {TABS.map((x) => (
        <Tabs.Screen
          key={x.name}
          name={x.name}
          options={{
            title: x.title,
            tabBarAccessibilityLabel: x.title,
            tabBarIcon: ({ color, size }) => <Ionicons name={x.icon} size={size} color={color as string} />,
          }}
        />
      ))}
      <Tabs.Screen name="search" options={{ href: null }} />
    </Tabs>
  )
}
