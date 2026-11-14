import { connect } from './db.ts'
import { createServer } from './server.ts'

const url = process.env.DATABASE_URL
if (!url) throw new Error('DATABASE_URL is required (role cnote_app, never the admin URL)')
const port = Number(process.env.COLLAB_PORT ?? 4300)
const app = createServer({
  pool: connect(url, Number(process.env.DB_POOL_MAX ?? 10)),
  debounceMs: process.env.COLLAB_DEBOUNCE_MS ? Number(process.env.COLLAB_DEBOUNCE_MS) : undefined,
})
app.server.listen(port, () => console.log(`collab listening on :${port}`))
for (const sig of ['SIGINT', 'SIGTERM'] as const)
  process.on(sig, () => void app.close().then(() => process.exit(0)))
