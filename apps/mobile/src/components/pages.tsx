import { useEffect, useState } from 'react'
import { KeyboardAvoidingView, Platform, ScrollView, SectionList, Text, TextInput, View } from 'react-native'
import { groupByDay } from '@/lib/format'
import { errMsg, useCmd, useRun } from '@/lib/hooks'
import { useTheme } from '@/lib/theme'
import { isPage, type Note, type Space } from '@/lib/types'
import { MasterDetail } from './master-detail'
import { Button, Fab, listPad, Refresh, Row, Screen, SectionHeader, StateView } from './ui'

export function PagesScreen() {
  const run = useRun()
  const notes = useCmd<Note[]>('note.list', { limit: 200 })
  const spaces = useCmd<Space[]>('space.list')
  const [creating, setCreating] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const pages = (notes.data ?? []).filter((n) => isPage(n.types))

  return (
    <MasterDetail
      base="pages"
      placeholder="Select a page"
      detail={(id) => <PageEditor id={id} onSaved={notes.refetch} />}
      list={(open, selected) => (
        <Screen>
          <SectionList
            sections={groupByDay(pages, (n) => n.updated_at)}
            keyExtractor={(n) => n.id}
            refreshControl={<Refresh refreshing={notes.refreshing} onRefresh={notes.refetch} />}
            contentContainerStyle={listPad}
            stickySectionHeadersEnabled
            renderSectionHeader={({ section }) => <SectionHeader title={section.title} />}
            renderItem={({ item }) => (
              <Row
                title={item.title || 'Untitled'}
                subtitle={
                  item.content_md
                    .replace(/[#*>`\-[\]]/g, '')
                    .trim()
                    .slice(0, 80) || 'No content'
                }
                selected={item.id === selected}
                onPress={() => open(item.id)}
                chevron
              />
            )}
            ListHeaderComponent={
              <StateView
                loading={notes.loading}
                error={notes.error ?? err}
                empty={!notes.loading && !notes.error && pages.length === 0}
                emptyText="No pages yet. Tap New page to write one."
                onRetry={notes.reload}
              />
            }
          />
          <Fab
            title="New page"
            icon="add"
            busy={creating}
            onPress={async () => {
              const space = spaces.data?.[0]
              if (!space) return setErr('No space to create the page in yet.')
              setCreating(true)
              setErr(null)
              try {
                const n = await run<Note>('note.create', { space_id: space.id, title: 'Untitled' })
                await notes.refetch()
                open(n.id)
              } catch (e) {
                setErr(errMsg(e))
              } finally {
                setCreating(false)
              }
            }}
          />
        </Screen>
      )}
    />
  )
}

/** Plain-markdown editor. Live co-editing (Yjs) is web-only; the app saves through `note.update`. */
export function PageEditor({ id, onSaved }: { id: string; onSaved?: () => void }) {
  const t = useTheme()
  const run = useRun()
  const note = useCmd<Note>('note.get', { id })
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [saved, setSaved] = useState<Note | null>(null)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    if (note.data) {
      setSaved(note.data)
      setTitle(note.data.title)
      setBody(note.data.content_md)
    }
  }, [note.data])

  if (!saved)
    return (
      <StateView loading={note.loading} error={note.error} empty={false} emptyText="" onRetry={note.reload} />
    )
  const dirty = title !== saved.title || body !== saved.content_md

  async function save() {
    setSaving(true)
    setErr(null)
    try {
      const n = await run<Note>('note.update', {
        id,
        title,
        content_md: body,
        expected_version: saved?.version,
      })
      setSaved(n)
      onSaved?.()
    } catch (e: any) {
      setErr(
        e?.code === 'conflict'
          ? 'Someone else changed this page. Pull to reload it, then re-apply your edit.'
          : e?.code === 'forbidden'
            ? 'You can view this page but not edit it. Ask its owner for edit access.'
            : errMsg(e),
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <ScrollView
        contentContainerStyle={{ padding: 16, gap: 12, paddingBottom: 120 }}
        keyboardShouldPersistTaps="handled"
        refreshControl={<Refresh refreshing={note.refreshing} onRefresh={note.refetch} />}
      >
        <TextInput
          accessibilityLabel="Page title"
          value={title}
          onChangeText={setTitle}
          placeholder="Title"
          placeholderTextColor={t.muted}
          style={{ color: t.fg, fontSize: 26, fontWeight: '700', minHeight: 44 }}
        />
        <TextInput
          accessibilityLabel="Page body, markdown"
          value={body}
          onChangeText={setBody}
          multiline
          placeholder="Write in markdown"
          placeholderTextColor={t.muted}
          textAlignVertical="top"
          style={{ color: t.fg, fontSize: 16, lineHeight: 24, minHeight: 280 }}
        />
        {err ? (
          <Text accessibilityRole="alert" style={{ color: t.bad }}>
            {err}
          </Text>
        ) : null}
        <View style={{ flexDirection: 'row', gap: 12, alignItems: 'center' }}>
          <Button title="Save" onPress={save} busy={saving} disabled={!dirty} />
          {!dirty ? <Text style={{ color: t.muted }}>Saved</Text> : null}
        </View>
      </ScrollView>
    </KeyboardAvoidingView>
  )
}
