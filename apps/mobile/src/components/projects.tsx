import { useState } from 'react'
import { FlatList, KeyboardAvoidingView, Platform, ScrollView, Text, View } from 'react-native'
import { errMsg, useCmd, useRun } from '@/lib/hooks'
import { useTheme } from '@/lib/theme'
import type { Comment, Item, ItemDetail, Space } from '@/lib/types'
import { MasterDetail } from './master-detail'
import { Button, Chip, Field, listPad, Pill, Refresh, Row, Screen, SectionHeader, StateView } from './ui'

const CATEGORIES = [
  { id: 'all', label: 'All' },
  { id: 'todo', label: 'To do' },
  { id: 'in_progress', label: 'In progress' },
  { id: 'done', label: 'Done' },
]
const tone = (c: string) => (c === 'done' ? 'ok' : c === 'in_progress' ? 'accent' : 'neutral')

export function ProjectsScreen() {
  const run = useRun()
  const t = useTheme()
  const items = useCmd<Item[]>('item.list', { limit: 200 })
  const spaces = useCmd<Space[]>('space.list')
  const [space, setSpace] = useState('all')
  const [cat, setCat] = useState('all')
  const [title, setTitle] = useState('')
  const [adding, setAdding] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const shown = (items.data ?? []).filter(
    (i) => (space === 'all' || i.space_id === space) && (cat === 'all' || i.status_category === cat),
  )
  const target = space !== 'all' ? space : spaces.data?.[0]?.id

  async function add() {
    if (!title.trim() || !target) return
    setAdding(true)
    setErr(null)
    try {
      await run('item.create', { space_id: target, title: title.trim() })
      setTitle('')
      await items.refetch()
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setAdding(false)
    }
  }

  return (
    <MasterDetail
      base="projects"
      placeholder="Select an item"
      detail={(id) => <ItemView id={id} onChanged={items.refetch} />}
      list={(open, selected) => (
        <Screen>
          <FlatList
            data={shown}
            keyExtractor={(i) => i.id}
            refreshControl={<Refresh refreshing={items.refreshing} onRefresh={items.refetch} />}
            contentContainerStyle={listPad}
            ListHeaderComponent={
              <View>
                <View style={{ flexDirection: 'row', gap: 8, padding: 12, alignItems: 'center' }}>
                  <Field
                    label="Quick add item"
                    value={title}
                    onChangeText={setTitle}
                    autoCapitalize="sentences"
                    returnKeyType="done"
                    onSubmitEditing={add}
                    style={{ flex: 1 }}
                  />
                  <Button title="Add" onPress={add} busy={adding} disabled={!title.trim() || !target} />
                </View>
                {err ? (
                  <Text
                    accessibilityRole="alert"
                    style={{ color: t.bad, paddingHorizontal: 12, paddingBottom: 8 }}
                  >
                    {err}
                  </Text>
                ) : null}
                <ScrollView
                  horizontal
                  showsHorizontalScrollIndicator={false}
                  contentContainerStyle={{ gap: 8, paddingHorizontal: 12, paddingBottom: 8 }}
                >
                  <Chip label="All spaces" active={space === 'all'} onPress={() => setSpace('all')} />
                  {(spaces.data ?? []).map((s) => (
                    <Chip key={s.id} label={s.name} active={space === s.id} onPress={() => setSpace(s.id)} />
                  ))}
                </ScrollView>
                <ScrollView
                  horizontal
                  showsHorizontalScrollIndicator={false}
                  contentContainerStyle={{ gap: 8, paddingHorizontal: 12, paddingBottom: 12 }}
                >
                  {CATEGORIES.map((c) => (
                    <Chip key={c.id} label={c.label} active={cat === c.id} onPress={() => setCat(c.id)} />
                  ))}
                </ScrollView>
                <StateView
                  loading={items.loading}
                  error={items.error}
                  empty={!items.loading && !items.error && shown.length === 0}
                  emptyText="No items match. Add one above."
                  onRetry={items.reload}
                />
              </View>
            }
            renderItem={({ item }) => (
              <Row
                title={item.title}
                subtitle={`${item.key} · ${item.priority}`}
                selected={item.id === selected}
                right={<Pill label={item.status} tone={tone(item.status_category)} />}
                onPress={() => open(item.id)}
              />
            )}
          />
        </Screen>
      )}
    />
  )
}

export function ItemView({ id, onChanged }: { id: string; onChanged?: () => void }) {
  const t = useTheme()
  const run = useRun()
  const item = useCmd<ItemDetail>('item.get', { id })
  const comments = useCmd<Comment[]>('comment.list', { note_id: id })
  const [busy, setBusy] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [body, setBody] = useState('')

  const i = item.data
  if (!i)
    return (
      <StateView loading={item.loading} error={item.error} empty={false} emptyText="" onRetry={item.reload} />
    )

  async function transition(to: string) {
    setBusy(to)
    setErr(null)
    try {
      await run('item.transition', { id, to })
      await item.refetch()
      onChanged?.()
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setBusy(null)
    }
  }

  async function comment() {
    if (!body.trim()) return
    setBusy('comment')
    setErr(null)
    try {
      await run('comment.add', { note_id: id, body: body.trim() })
      setBody('')
      await comments.refetch()
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setBusy(null)
    }
  }

  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <ScrollView
        contentContainerStyle={{ padding: 16, gap: 12, paddingBottom: 120 }}
        keyboardShouldPersistTaps="handled"
        refreshControl={
          <Refresh
            refreshing={item.refreshing}
            onRefresh={() => {
              void item.refetch()
              void comments.refetch()
            }}
          />
        }
      >
        <Text style={{ color: t.muted, fontSize: 13, fontWeight: '600' }}>{i.key}</Text>
        <Text style={{ color: t.fg, fontSize: 24, fontWeight: '700' }} accessibilityRole="header">
          {i.title}
        </Text>
        <View style={{ flexDirection: 'row', gap: 8 }}>
          <Pill label={i.status} tone={tone(i.status_category)} />
          <Pill label={i.priority} />
          <Pill label={i.item_type} />
        </View>
        {i.description ? (
          <Text style={{ color: t.fg, fontSize: 16, lineHeight: 24 }}>{i.description}</Text>
        ) : null}

        {i.transitions.length ? (
          <>
            <SectionHeader title="Move to" />
            <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 8 }}>
              {i.transitions.map((x) => (
                <Button
                  key={x.to}
                  title={x.to}
                  kind="plain"
                  busy={busy === x.to}
                  disabled={!!busy}
                  onPress={() => transition(x.to)}
                />
              ))}
            </View>
          </>
        ) : null}
        {err ? (
          <Text accessibilityRole="alert" style={{ color: t.bad }}>
            {err}
          </Text>
        ) : null}

        <SectionHeader title={`Comments${comments.data ? ` (${comments.data.length})` : ''}`} />
        {comments.error ? <Text style={{ color: t.bad }}>{comments.error}</Text> : null}
        {(comments.data ?? []).map((c) => (
          <View key={c.id} style={{ gap: 2 }}>
            <Text style={{ color: t.muted, fontSize: 13 }}>
              {`${c.author_name} · ${new Date(c.created_at).toLocaleString('en-IN')}`}
            </Text>
            <Text style={{ color: t.fg, fontSize: 15 }}>{c.body}</Text>
          </View>
        ))}
        {comments.data?.length === 0 ? <Text style={{ color: t.muted }}>No comments yet.</Text> : null}
        <Field
          label="Add a comment"
          value={body}
          onChangeText={setBody}
          multiline
          autoCapitalize="sentences"
          style={{ minHeight: 72, paddingTop: 10 }}
        />
        <Button
          title="Comment"
          onPress={comment}
          busy={busy === 'comment'}
          disabled={!body.trim() || !!busy}
        />
      </ScrollView>
    </KeyboardAvoidingView>
  )
}
