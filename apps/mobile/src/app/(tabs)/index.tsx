import { Ionicons } from '@expo/vector-icons'
import { useRouter } from 'expo-router'
import { useState } from 'react'
import { Modal, Pressable, ScrollView, Text, View } from 'react-native'
import { SafeAreaView } from 'react-native-safe-area-context'
import { Row, SectionHeader } from '@/components/ui'
import { useSession } from '@/lib/session'
import { TAB_CLEARANCE, useTheme } from '@/lib/theme'

type Icon = keyof typeof Ionicons.glyphMap
const MODULES: { href: string; title: string; hint: string; icon: Icon; color: string }[] = [
  { href: '/pages', title: 'Pages', hint: 'Notes and living docs', icon: 'document-text', color: '#4f46e5' },
  { href: '/projects', title: 'Projects', hint: 'Work items and status', icon: 'albums', color: '#0ea5e9' },
  { href: '/crm', title: 'CRM', hint: 'Contacts and deals', icon: 'people', color: '#16a34a' },
  { href: '/billing', title: 'Billing', hint: 'GST invoices', icon: 'receipt', color: '#d97706' },
  { href: '/search', title: 'Search', hint: 'Find anything', icon: 'search', color: '#9333ea' },
]

function Badge({ icon, color }: { icon: Icon; color: string }) {
  return (
    <View
      style={{
        width: 32,
        height: 32,
        borderRadius: 8,
        backgroundColor: color,
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <Ionicons name={icon} size={18} color="#fff" />
    </View>
  )
}

export default function Home() {
  const t = useTheme()
  const router = useRouter()
  const { me, wsId, setWorkspace, signOut, server } = useSession()
  const [picker, setPicker] = useState(false)
  const ws = me?.workspaces.find((w) => w.id === wsId)

  return (
    <SafeAreaView edges={['top']} style={{ flex: 1, backgroundColor: t.side }}>
      <ScrollView contentContainerStyle={{ paddingBottom: TAB_CLEARANCE + 24, alignItems: 'center' }}>
        <View style={{ width: '100%', maxWidth: 640 }}>
          <Text
            style={{ color: t.fg, fontSize: 32, fontWeight: '700', padding: 16 }}
            accessibilityRole="header"
          >
            Home
          </Text>

          <SectionHeader title="Workspace" />
          <Row
            title={ws?.name ?? 'No workspace'}
            subtitle={ws ? `${ws.role} · tap to switch` : undefined}
            left={<Badge icon="business" color={t.accent} />}
            chevron
            onPress={() => setPicker(true)}
            label={`Workspace ${ws?.name ?? 'none'}. Switch workspace`}
          />

          <SectionHeader title="Modules" />
          {MODULES.map((m) => (
            <Row
              key={m.href}
              title={m.title}
              subtitle={m.hint}
              left={<Badge icon={m.icon} color={m.color} />}
              chevron
              onPress={() => router.push(m.href as never)}
            />
          ))}

          <SectionHeader title="Account" />
          <Row title={me?.user.name ?? ''} subtitle={`${me?.user.email ?? ''} · ${server}`} />
          <Row
            title="Sign out"
            left={<Badge icon="log-out" color={t.bad} />}
            onPress={() => void signOut()}
          />
        </View>
      </ScrollView>

      <Modal visible={picker} transparent animationType="slide" onRequestClose={() => setPicker(false)}>
        <Pressable
          style={{ flex: 1, backgroundColor: '#0006' }}
          onPress={() => setPicker(false)}
          accessibilityLabel="Close workspace picker"
        />
        <SafeAreaView
          edges={['bottom']}
          style={{ backgroundColor: t.canvas, borderTopLeftRadius: 16, borderTopRightRadius: 16 }}
        >
          <Text
            style={{ color: t.fg, fontSize: 17, fontWeight: '600', padding: 16 }}
            accessibilityRole="header"
          >
            Switch workspace
          </Text>
          {me?.workspaces.map((w) => (
            <Row
              key={w.id}
              title={w.name}
              subtitle={w.role}
              selected={w.id === wsId}
              right={w.id === wsId ? <Ionicons name="checkmark" size={20} color={t.accent} /> : undefined}
              onPress={() => {
                setWorkspace(w.id)
                setPicker(false)
              }}
            />
          ))}
        </SafeAreaView>
      </Modal>
    </SafeAreaView>
  )
}
