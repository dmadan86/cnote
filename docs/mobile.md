# Mobile and tablet app

`apps/mobile` (`@cnote/mobile`) is an Expo SDK 58 / React Native 0.88 / expo-router 58 app, one codebase for
phone and tablet. It is a thin client over the same command API as the web app: every screen calls
`POST /api/w/{workspace}/commands/{name}`.

Screens: sign in / sign up, Home (modules, workspace switcher, sign out), Pages (list by day, view and edit),
Projects (spaces and status filters, item detail with status transitions and comments, quick add),
CRM (contacts, deals by stage), Billing (invoices, read-only) and Search.

## Run

```sh
cargo run -p cnote-server -- serve --with-worker        # API on :4000
pnpm install
cp apps/mobile/.env.example apps/mobile/.env            # optional: EXPO_PUBLIC_API_URL
pnpm dev:mobile                                         # Metro on :8081
cd apps/mobile && pnpm exec expo run:ios                # or run:android; needs Xcode / Android Studio
```

The server URL is also an input on the sign-in screen and is remembered. Android emulators reach the host
as `http://10.0.2.2:4000`; a physical device needs your machine's LAN address. Plain `http` is only for
development, use https for anything real.

Checks (also the `mobile` CI job): `pnpm lint`, `pnpm --filter @cnote/mobile typecheck`,
`pnpm --filter @cnote/mobile test`, `pnpm --filter @cnote/mobile exec expo export --platform ios --platform android`.
If `jest` hangs on a machine with a broken watchman install, run `pnpm --filter @cnote/mobile test --watchman=false`.

## Architecture

- `src/lib/api.ts`: fetch client. Sends `Authorization: Bearer cs_...`; login and signup send
  `x-cnote-client: native` so the response carries the session token. Errors map to `ApiError{code,message,status}`
  (`network` and `bad_response` are client-side codes).
- `src/lib/session.tsx`: server URL, token and active workspace live in `expo-secure-store`. A 401 from any
  call drops the session (`useRun` in `hooks.ts`), which flips the `Stack.Protected` guard back to sign-in. An
  offline launch keeps the stored token.
- `src/lib/layout.ts` and `src/components/master-detail.tsx`: width >= 768dp renders list | detail side by side
  (selection is local state); below that the row pushes `/{module}/{id}`. The pushed route and the pane share
  one detail component.
- Metro runs with the default Expo config; Expo 58 resolves pnpm's isolated `node_modules`, so no
  `node-linker=hoisted` is needed and the web app's install is unaffected.
- Theme tokens in `src/lib/theme.ts` mirror `apps/web/src/app/globals.css`; light and dark follow the system.

## Limits

- Page editing is a plain markdown `TextInput` saved through `note.update` with `expected_version`; a concurrent
  change shows a conflict message. Live Yjs collaboration is web-only.
- Deals and billing are read-only; items support status transitions, comments and quick create. No offline cache.
- No push notifications, deep-link handling beyond the `cnote://` scheme, or app-store assets.

## Build and release (later)

Use EAS Build (`eas build -p ios|android`) with a production `EXPO_PUBLIC_API_URL`; signing credentials stay in
EAS, never in the repo.
