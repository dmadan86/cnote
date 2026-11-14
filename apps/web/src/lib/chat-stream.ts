// Live chat updates: one EventSource per workspace shared by everything on the page (chat and the sidebar badge).
// Events are hints; listeners refetch. While the stream is down, listeners get a 'poll' every few seconds instead,
// and a 'resync' whenever it (re)connects.
export type ChatEvent =
  | { k: 'message' | 'reaction' | 'read' | 'resync' | 'poll'; c?: string; m?: string; p?: string }
  | { k: 'status'; live: boolean }
type Fn = (e: ChatEvent) => void
type Hub = { fns: Set<Fn>; live: boolean; stop: () => void }

const POLL_MS = 4000
const RETRY_MS = 30_000
const hubs = new Map<string, Hub>()

function start(ws: string): Hub {
  const hub: Hub = { fns: new Set(), live: false, stop: () => {} }
  const emit: Fn = (e) => {
    for (const f of hub.fns) f(e)
  }
  const setLive = (v: boolean) => {
    if (hub.live === v) return
    hub.live = v
    emit({ k: 'status', live: v })
  }
  let es: EventSource | undefined
  let retry: ReturnType<typeof setTimeout> | undefined
  const connect = () => {
    if (typeof EventSource === 'undefined') return
    es = new EventSource(`/api/w/${ws}/chat/stream`)
    es.addEventListener('ready', () => {
      setLive(true)
      emit({ k: 'resync' }) // whatever happened since the page loaded or the stream dropped
    })
    es.addEventListener('chat', (m) => {
      try {
        emit(JSON.parse((m as MessageEvent).data))
      } catch {}
    })
    es.onerror = () => {
      setLive(false)
      if (es?.readyState === EventSource.CLOSED) {
        // refused (401/403/5xx): the browser will not retry, so polling carries on and we try again later
        es.close()
        retry = setTimeout(connect, RETRY_MS)
      }
    }
  }
  connect()
  const poll = setInterval(() => {
    if (!hub.live && document.visibilityState === 'visible') emit({ k: 'poll' })
  }, POLL_MS)
  hub.stop = () => {
    clearInterval(poll)
    clearTimeout(retry)
    es?.close()
  }
  return hub
}

export function subscribeChat(ws: string, fn: Fn): () => void {
  const hub = hubs.get(ws) ?? start(ws)
  hubs.set(ws, hub)
  hub.fns.add(fn)
  fn({ k: 'status', live: hub.live })
  return () => {
    hub.fns.delete(fn)
    if (hub.fns.size === 0) {
      hub.stop()
      hubs.delete(ws)
    }
  }
}
