import { FlatList, ScrollView, Text, View } from 'react-native'
import { formatInr } from '@/lib/format'
import { useCmd } from '@/lib/hooks'
import { useTheme } from '@/lib/theme'
import type { Invoice, InvoiceDetail } from '@/lib/types'
import { MasterDetail } from './master-detail'
import { listPad, Pill, Refresh, Row, Screen, SectionHeader, StateView } from './ui'

const statusTone = (s: string) =>
  s === 'paid' ? 'ok' : s === 'draft' ? 'neutral' : s === 'cancelled' ? 'bad' : 'warn'
const title = (i: Invoice) => i.number ?? (i.kind === 'credit_note' ? 'Credit note (draft)' : 'Draft invoice')

export function BillingScreen() {
  const list = useCmd<Invoice[]>('invoice.list', { limit: 200 })
  return (
    <MasterDetail
      base="billing"
      placeholder="Select an invoice"
      detail={(id) => <InvoiceView id={id} />}
      list={(open, selected) => (
        <Screen>
          <FlatList
            data={list.data ?? []}
            keyExtractor={(i) => i.id}
            refreshControl={<Refresh refreshing={list.refreshing} onRefresh={list.refetch} />}
            contentContainerStyle={listPad}
            ListHeaderComponent={
              <StateView
                loading={list.loading}
                error={list.error}
                empty={!list.loading && !list.error && (list.data?.length ?? 0) === 0}
                emptyText="No invoices yet."
                onRetry={list.reload}
              />
            }
            renderItem={({ item }) => (
              <Row
                title={title(item)}
                subtitle={`Due ${item.due_date}`}
                selected={item.id === selected}
                left={<Pill label={item.status} tone={statusTone(item.status)} />}
                right={<Text style={{ fontWeight: '600' }}>{formatInr(item.total_minor)}</Text>}
                onPress={() => open(item.id)}
              />
            )}
          />
        </Screen>
      )}
    />
  )
}

export function InvoiceView({ id }: { id: string }) {
  const t = useTheme()
  const q = useCmd<InvoiceDetail>('invoice.get', { id })
  const i = q.data
  if (!i)
    return <StateView loading={q.loading} error={q.error} empty={false} emptyText="" onRetry={q.reload} />

  const totals: [string, number, boolean?][] = [
    ['Subtotal', i.subtotal_minor],
    ...(i.cgst_minor
      ? ([
          ['CGST', i.cgst_minor],
          ['SGST', i.sgst_minor],
        ] as [string, number][])
      : []),
    ...(i.igst_minor ? ([['IGST', i.igst_minor]] as [string, number][]) : []),
    ['Total', i.total_minor, true],
    ['Paid', i.paid_minor],
    ['Outstanding', i.outstanding_minor, true],
  ]
  return (
    <ScrollView
      contentContainerStyle={{ padding: 16, gap: 8, paddingBottom: 120 }}
      refreshControl={<Refresh refreshing={q.refreshing} onRefresh={q.refetch} />}
    >
      <Text style={{ color: t.fg, fontSize: 24, fontWeight: '700' }} accessibilityRole="header">
        {title(i)}
      </Text>
      <View style={{ flexDirection: 'row', gap: 8 }}>
        <Pill label={i.status} tone={statusTone(i.status)} />
      </View>
      <Text
        style={{ color: t.muted }}
      >{`${i.contact_name} · dated ${i.invoice_date} · due ${i.due_date}`}</Text>

      <SectionHeader title="Items" />
      {i.lines.map((l) => (
        <Row
          key={l.id}
          title={l.description}
          subtitle={`${l.quantity} × ${formatInr(l.unit_price_minor)}`}
          right={<Text style={{ color: t.fg }}>{formatInr(l.taxable_minor)}</Text>}
        />
      ))}

      <SectionHeader title="Totals" />
      {totals.map(([k, v, bold]) => (
        <View
          key={k}
          style={{
            flexDirection: 'row',
            justifyContent: 'space-between',
            minHeight: 32,
            alignItems: 'center',
          }}
        >
          <Text style={{ color: bold ? t.fg : t.muted, fontWeight: bold ? '700' : '400' }}>{k}</Text>
          <Text style={{ color: t.fg, fontWeight: bold ? '700' : '400' }}>{formatInr(v)}</Text>
        </View>
      ))}
    </ScrollView>
  )
}
