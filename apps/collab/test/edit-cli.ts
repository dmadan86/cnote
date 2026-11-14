// Used by the Go differential tests (apps/api): connect to the collab service as a user and type into a note, so
// the edits sit in core.note_updates (a long debounce keeps them out of the snapshot).
// usage: tsx test/edit-cli.ts <port> <workspace> <note> <session-cookie> <text>...
import * as Y from 'yjs'
import { append, open, sleep } from './helpers.ts'

const [port, ws, note, cookie, ...texts] = process.argv.slice(2)
const s = await open(Number(port), { ws, note } as never, { cookie })
const doc = new Y.Doc()
await s.sync(doc)
for (const t of texts) {
  s.send(2, append(doc, t))
  await sleep(150)
}
await sleep(300)
s.close()
process.exit(0)
