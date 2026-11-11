import Constants from 'expo-constants'
import * as SecureStore from 'expo-secure-store'
import { createContext, type ReactNode, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { type Api, createApi, type Me, normalizeUrl } from './api'

export const DEFAULT_SERVER =
  process.env.EXPO_PUBLIC_API_URL ??
  (Constants.expoConfig?.extra?.apiUrl as string | undefined) ??
  'http://localhost:4000'

const K = { server: 'cnote.server', token: 'cnote.token', ws: 'cnote.ws' }
const read = (k: string) => SecureStore.getItemAsync(k).catch(() => null)
const write = (k: string, v: string | null) =>
  (v === null ? SecureStore.deleteItemAsync(k) : SecureStore.setItemAsync(k, v)).catch(() => {})

type Session = {
  ready: boolean
  server: string
  token: string | null
  me: Me | null
  wsId: string | null
  api: Api
  signIn: (server: string, email: string, password: string) => Promise<void>
  signUp: (
    server: string,
    b: { name: string; email: string; password: string; workspace_name: string },
  ) => Promise<void>
  signOut: () => Promise<void>
  /** Drop the local session without calling the server (token already rejected). */
  expire: () => void
  setWorkspace: (id: string) => void
  refreshMe: () => Promise<void>
}

const Ctx = createContext<Session | null>(null)

export function SessionProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false)
  const [server, setServer] = useState(DEFAULT_SERVER)
  const [token, setToken] = useState<string | null>(null)
  const [me, setMe] = useState<Me | null>(null)
  const [wsId, setWsId] = useState<string | null>(null)
  const api = useMemo(() => createApi({ baseUrl: server, token }), [server, token])

  const adopt = useCallback((m: Me, saved: string | null) => {
    setMe(m)
    const id = m.workspaces.find((w) => w.id === saved)?.id ?? m.workspaces[0]?.id ?? null
    setWsId(id)
    void write(K.ws, id)
  }, [])

  useEffect(() => {
    ;(async () => {
      const [s, t, w] = await Promise.all([read(K.server), read(K.token), read(K.ws)])
      const url = s ?? DEFAULT_SERVER
      setServer(url)
      if (t) {
        try {
          adopt(await createApi({ baseUrl: url, token: t }).me(), w)
          setToken(t)
        } catch (e: any) {
          // Keep the token on a network blip so an offline launch is not a sign-out; drop it when rejected.
          if (e?.status === 401) void write(K.token, null)
          else setToken(t)
        }
      }
      setReady(true)
    })()
  }, [adopt])

  const finish = useCallback(
    async (url: string, t: string) => {
      await Promise.all([write(K.server, url), write(K.token, t)])
      const m = await createApi({ baseUrl: url, token: t }).me()
      setServer(url)
      setToken(t)
      adopt(m, null)
    },
    [adopt],
  )

  const value = useMemo<Session>(
    () => ({
      ready,
      server,
      token,
      me,
      wsId,
      api,
      signIn: async (s, email, password) => {
        const url = normalizeUrl(s)
        const r = await createApi({ baseUrl: url }).login({ email, password })
        await finish(url, r.token)
      },
      signUp: async (s, b) => {
        const url = normalizeUrl(s)
        const r = await createApi({ baseUrl: url }).signup(b)
        await finish(url, r.token)
      },
      signOut: async () => {
        await api.logout().catch(() => {})
        await Promise.all([write(K.token, null), write(K.ws, null)])
        setToken(null)
        setMe(null)
        setWsId(null)
      },
      expire: () => {
        void write(K.token, null)
        setToken(null)
        setMe(null)
      },
      setWorkspace: (id) => {
        setWsId(id)
        void write(K.ws, id)
      },
      refreshMe: async () => adopt(await api.me(), wsId),
    }),
    [ready, server, token, me, wsId, api, finish, adopt],
  )
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useSession() {
  const s = useContext(Ctx)
  if (!s) throw new Error('useSession outside SessionProvider')
  return s
}
