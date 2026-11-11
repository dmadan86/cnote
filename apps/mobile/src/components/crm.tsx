import { useState } from 'react'
import { FlatList, ScrollView, SectionList, Text, View } from 'react-native'
import { formatInr } from '@/lib/format'
import { useCmd } from '@/lib/hooks'
import { useTheme } from '@/lib/theme'
import { type Contact, DEAL_STAGES, type Deal } from '@/lib/types'
import { MasterDetail } from './master-detail'
import { Chip, Field, listPad, Pill, Refresh, Row, Screen, SectionHeader, StateView } from './ui'

export function CrmScreen() {
  const [tab, setTab] = useState<'contacts' | 'deals'>('contacts')
  const [q, setQ] = useState('')
  const contacts = useCmd<Contact[]>('contact.list', { q: q.trim() || null, limit: 200 })
  const deals = useCmd<Deal[]>('deal.list', { limit: 200 })

  const stageSections = DEAL_STAGES.map((stage) => ({
    title: stage,
    data: (deals.data ?? []).filter((d) => d.stage === stage),
  })).filter((s) => s.data.length)

  const switcher = (
    <View style={{ flexDirection: 'row', gap: 8, padding: 12 }}>
      <Chip label="Contacts" active={tab === 'contacts'} onPress={() => setTab('contacts')} />
      <Chip label="Deals" active={tab === 'deals'} onPress={() => setTab('deals')} />
    </View>
  )

  return (
    <MasterDetail
      base="crm"
      placeholder="Select a contact"
      detail={(id) => <ContactView id={id} />}
      list={(open, selected) => (
        <Screen>
          {tab === 'contacts' ? (
            <FlatList
              data={contacts.data ?? []}
              keyExtractor={(c) => c.id}
              refreshControl={<Refresh refreshing={contacts.refreshing} onRefresh={contacts.refetch} />}
              contentContainerStyle={listPad}
              keyboardShouldPersistTaps="handled"
              ListHeaderComponent={
                <>
                  {switcher}
                  <View style={{ paddingHorizontal: 12, paddingBottom: 8 }}>
                    <Field label="Search contacts" value={q} onChangeText={setQ} returnKeyType="search" />
                  </View>
                  <StateView
                    loading={contacts.loading}
                    error={contacts.error}
                    empty={!contacts.loading && !contacts.error && (contacts.data?.length ?? 0) === 0}
                    emptyText="No contacts yet."
                    onRetry={contacts.reload}
                  />
                </>
              }
              renderItem={({ item }) => (
                <Row
                  title={item.name}
                  subtitle={item.email ?? item.company ?? item.phone ?? undefined}
                  selected={item.id === selected}
                  onPress={() => open(item.id)}
                  chevron
                />
              )}
            />
          ) : (
            <SectionList
              sections={stageSections}
              keyExtractor={(d) => d.id}
              refreshControl={<Refresh refreshing={deals.refreshing} onRefresh={deals.refetch} />}
              contentContainerStyle={listPad}
              renderSectionHeader={({ section }) => (
                <SectionHeader
                  title={`${section.title} · ${section.data.length} · ${formatInr(section.data.reduce((a, d) => a + d.amount_minor, 0))}`}
                />
              )}
              ListHeaderComponent={
                <>
                  {switcher}
                  <StateView
                    loading={deals.loading}
                    error={deals.error}
                    empty={!deals.loading && !deals.error && stageSections.length === 0}
                    emptyText="No deals yet."
                    onRetry={deals.reload}
                  />
                </>
              }
              renderItem={({ item }) => (
                <Row
                  title={item.title}
                  subtitle={`${item.probability}% · ${item.expected_close ?? 'no close date'}`}
                  right={<Text style={{ fontWeight: '600' }}>{formatInr(item.amount_minor)}</Text>}
                />
              )}
            />
          )}
        </Screen>
      )}
    />
  )
}

export function ContactView({ id }: { id: string }) {
  const t = useTheme()
  const c = useCmd<Contact>('contact.get', { id })
  const deals = useCmd<Deal[]>('deal.list', { contact_id: id })
  if (!c.data)
    return <StateView loading={c.loading} error={c.error} empty={false} emptyText="" onRetry={c.reload} />
  const x = c.data
  const fields: [string, string | null][] = [
    ['Email', x.email],
    ['Phone', x.phone],
    ['Company', x.company],
    ['GSTIN', x.gstin],
    ['State code', x.state_code],
  ]
  return (
    <ScrollView
      contentContainerStyle={{ padding: 16, gap: 8, paddingBottom: 120 }}
      refreshControl={
        <Refresh
          refreshing={c.refreshing}
          onRefresh={() => {
            void c.refetch()
            void deals.refetch()
          }}
        />
      }
    >
      <Text style={{ color: t.fg, fontSize: 24, fontWeight: '700' }} accessibilityRole="header">
        {x.name}
      </Text>
      <Pill label={x.kind} />
      {fields.map(([k, v]) => (
        <View
          key={k}
          style={{
            flexDirection: 'row',
            justifyContent: 'space-between',
            minHeight: 36,
            alignItems: 'center',
          }}
        >
          <Text style={{ color: t.muted }}>{k}</Text>
          <Text style={{ color: t.fg }} selectable>
            {v || '—'}
          </Text>
        </View>
      ))}
      <SectionHeader title="Deals" />
      {(deals.data ?? []).map((d) => (
        <Row
          key={d.id}
          title={d.title}
          subtitle={d.stage}
          right={<Text style={{ fontWeight: '600' }}>{formatInr(d.amount_minor)}</Text>}
        />
      ))}
      {deals.data?.length === 0 ? <Text style={{ color: t.muted }}>No deals for this contact.</Text> : null}
    </ScrollView>
  )
}
