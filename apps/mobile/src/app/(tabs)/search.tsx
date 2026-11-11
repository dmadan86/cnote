import { Ionicons } from '@expo/vector-icons'
import { useRouter } from 'expo-router'
import { useEffect, useState } from 'react'
import { FlatList, View } from 'react-native'
import { SafeAreaView } from 'react-native-safe-area-context'
import { Field, listPad, Pill, Refresh, Row, StateView } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { useTheme } from '@/lib/theme'
import { routeFor, type SearchHit } from '@/lib/types'

export default function Search() {
  const t = useTheme()
  const router = useRouter()
  const [text, setText] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => {
    const h = setTimeout(() => setQ(text.trim()), 300)
    return () => clearTimeout(h)
  }, [text])
  const hits = useCmd<SearchHit[]>('search.notes', { q, limit: 30 }, q.length > 0)

  return (
    <SafeAreaView edges={['top']} style={{ flex: 1, backgroundColor: t.canvas }}>
      <View style={{ padding: 12, flexDirection: 'row', alignItems: 'center', gap: 8 }}>
        <Ionicons name="search" size={20} color={t.muted} />
        <Field
          label="Search notes"
          value={text}
          onChangeText={setText}
          returnKeyType="search"
          autoFocus
          style={{ flex: 1 }}
        />
      </View>
      <FlatList
        data={q ? (hits.data ?? []) : []}
        keyExtractor={(h) => h.id}
        keyboardShouldPersistTaps="handled"
        contentContainerStyle={listPad}
        refreshControl={<Refresh refreshing={hits.refreshing} onRefresh={hits.refetch} />}
        ListHeaderComponent={
          q ? (
            <StateView
              loading={hits.loading}
              error={hits.error}
              empty={!hits.loading && !hits.error && (hits.data?.length ?? 0) === 0}
              emptyText={`Nothing found for “${q}”.`}
              onRetry={hits.reload}
            />
          ) : (
            <StateView
              loading={false}
              error={null}
              empty
              emptyText="Search pages, items, contacts and invoices."
              onRetry={() => {}}
            />
          )
        }
        renderItem={({ item }) => (
          <Row
            title={item.title || 'Untitled'}
            subtitle={item.snippet || undefined}
            right={item.types[0] ? <Pill label={item.types[0]} /> : undefined}
            onPress={() => router.push(routeFor(item.types, item.id) as never)}
            chevron
          />
        )}
      />
    </SafeAreaView>
  )
}
