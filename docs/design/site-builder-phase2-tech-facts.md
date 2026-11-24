# Site builder phase 2: tech facts

Researched 2026-11-24 against `apps/web` (Next 16.4.6, React 19.3.1, Tailwind 4). Each fact is marked **[V]** verified today from the cited source, or **[U]** unverified (recalled, inferred, or not tested here).

## 1. Puck editor

### Package, version, licence

- **[V]** The package is `@puckeditor/core`. `@measured/puck` is deprecated on npm ("Puck has moved. Please use @puckeditor/core instead") and stopped at 0.20.2. Source: `npm view @measured/puck`, https://www.npmjs.com/package/@puckeditor/core
- **[V]** Latest stable is **0.24.2**, published 2026-11-23 (one day old). 0.24.1 is from 2026-11-18 and 0.24.0 from 2026-10-29. The only other dist-tag is `canary`. Source: https://github.com/puckeditor/puck/releases
- **[V]** Licence is MIT. Repo: https://github.com/puckeditor/puck
- **[V]** Peer dependencies are `react: ^18.0.0 || ^19.0.0` and nothing else. There is no `next` or `react-dom` peer. Engines: Node >= 20. React 19.3.1 satisfies the range.
- **[U]** Next 16 is not declared anywhere as supported or unsupported. Puck's own repo bumped its apps to Next 16.x (PRs #1452, #1597), and I found no open issue about Next 16 or React 19. Not tested in our app.

Install line (exact pin):

```
pnpm --filter @cnote/web add -E @puckeditor/core@0.24.2
```

### Things to know before installing

- **[V]** 0.24.2 lists `@puckeditor/cli: "latest"` as a runtime dependency (an unpinned tag), plus `happy-dom` and `@vercel/detect-agent`. 0.24.1 added an "agent-friendly Puck CLI" bin, marked experimental. The CLI's current `latest` is a canary build (`0.25.0-canary.…`). Our lockfile will pin it, but this is a supply-chain point worth a look given the stable-versions-only rule.
- **[V]** It depends on `@tiptap/*` `^3.30.4` and `@dnd-kit/react` 0.4.0. We already ship tiptap `^3.33.1`, so tiptap should dedupe. `@dnd-kit/react` is a different package from our `@dnd-kit/core` 6.3.1, so both will be in the bundle.
- **[V]** Bundle size: 633 kB minified, 174 kB gzip for the main entry (Bundlephobia, 0.24.2). Tiptap is counted in that figure, so our real increase is smaller. **[U]** The real increase is not measured.
- **[V]** By default `puck.css` loads the Inter font from a Puck-hosted CDN (`cdn.puck.run`, changed in 0.24.2). Import `@puckeditor/core/no-external.css` instead to avoid the external request. Source: https://puckeditor.com/docs/extending-puck/theming

### API facts

- **Config** **[V]**: `config.components[Name]` takes `render`, `fields`, `defaultProps`, `label`, `inline`, `permissions`, `resolveData`, `resolveFields`, `resolvePermissions`, `metadata`, `renderFields`. `render` receives the field props plus `id` and `puck: { isEditing, dragRef, metadata }`. Source: https://puckeditor.com/docs/api-reference/configuration/component-config
- **Built-in fields** **[V]**: text, textarea, number, select, radio, checkbox (new in 0.24), richtext, array, object, external, slot, custom. There is **no built-in image or colour field**.
- **Custom fields** **[V]**: `{ type: "custom", render: ({ name, id, value, onChange, field }) => … }`. `onChange(value, ui?)` sets the value. Wrap with `FieldLabel` from `@puckeditor/core`. Our image picker and colour input go here. Source: https://puckeditor.com/docs/api-reference/fields/custom
- **Array fields** **[V]**: `arrayFields`, `defaultItemProps`, `getItemSummary(item, index)`, `min`, `max`. Drag-to-reorder is built in. Source: https://puckeditor.com/docs/api-reference/fields/array
- **Iframe** **[V]**: the canvas renders in an iframe by default: `iframe={{ enabled: true, waitForStyles: true, syncHostStyles: true }}`. `syncHostStyles` copies the host page's `<style>` and stylesheet tags into the iframe, so our Tailwind output arrives without extra work. Set it to `false` to isolate the canvas, then inject the site's own stylesheet through the `iframe` override, which receives `{ children, document }`. Disabling the iframe also disables viewports. Sources: https://puckeditor.com/docs/api-reference/components/puck, https://puckeditor.com/docs/api-reference/overrides/iframe
- **Data** **[V]**: `data` is the *initial* value only ("Can't change after mount"). `onChange(data)` fires on every edit and `onPublish(data)` on the Publish button. Puck owns the state, so it is not a controlled component. To reset, remount with a new `key` (**[U]** standard React approach, not from the docs).
- **Replacing the UI** **[V]**: two routes.
  - Composition: pass children to `<Puck>` and place `<Puck.Preview />`, `<Puck.Fields />`, `<Puck.Components />`, `<Puck.Outline />` in our own layout. The default header and outline are simply not rendered. Source: https://puckeditor.com/docs/extending-puck/composition
  - `overrides` (marked experimental): `header`, `headerActions`, `outline`, `drawer`, `drawerItem`, `fields`, `fieldLabel`, `fieldTypes`, `actionBar`, `componentOverlay`, `iframe`, `preview`, `puck`. Source: https://puckeditor.com/docs/api-reference/overrides
  - Our own save button reads state through `usePuck()` (`appState`, `dispatch`, `history`, `selectedItem`). Source: https://puckeditor.com/docs/api-reference/puck-api
- **Theming** **[V]**: CSS custom properties such as `--puck-color-interactive`, `--puck-radius-m`, `--puck-header-color-bg`, set on an element above the editor. Dark mode is still an open request (#278).

### Next App Router and RSC

- **[V]** `<Puck>` cannot run in a server component. `<Render>` and `resolveAllData` can, if the config has no client-only code. The package has a `react-server` export condition and an explicit `@puckeditor/core/rsc` entry. Sources: https://puckeditor.com/docs/integrating-puck/server-components, `npm view @puckeditor/core@0.24.2 exports`
- **[V]** If components need hooks, either mark those component files `"use client"` or keep two configs (client config for `<Puck>`, render-only server config for `<Render>`). The server config must keep `richtext` and `slot` fields or they render wrongly.
- **[V]** Open issues that could affect us: #1906 no CSP nonce for the `<style>` tags Puck injects; #1884 children of `<Puck>` remount once after the iframe mounts; #1885 slot content remounts on every `<Render>` re-render; #1871 typing in an array item re-syncs the whole array per keystroke; #1849 richtext inside an array is broken.

### Integration sketch

1. One block config module maps each of our existing block renderers to a Puck component (`fields` + `render` calling our renderer). No hooks in it, so the public site keeps rendering with our renderer or `<Render>` on the server.
2. A `"use client"` editor component, loaded with `next/dynamic` on the editor route only, mounts `<Puck config data onChange>` with composition children: our header, `Puck.Components`, `Puck.Preview`, `Puck.Fields`.
3. `onChange` feeds our existing autosave; our own Publish button calls our API with `usePuck().appState.data`.
4. Image and colour inputs are `custom` fields wrapping the pickers we already have.
5. Import `no-external.css`. Leave `syncHostStyles` on unless the app's global CSS visibly leaks into the canvas.

**[U]** Our stored page format versus Puck's `Data` shape (`{ root, content, zones }`) needs a mapping decision; not examined here.

## 2. Caddy on-demand TLS

Current release: **v2.11.9** (2026-11-05). **[V]** https://github.com/caddyserver/caddy/releases

### The `ask` contract **[V]**

Source: https://caddyserver.com/docs/caddyfile/options#on-demand-tls

- Caddy makes an HTTP request to the URL with `?domain=<name>` in the query string. **[U]** The method is GET (the docs say only "an HTTP request").
- Any `2xx` allows issuance. Any other status cancels issuance and fails the TLS handshake.
- The endpoint "should return as fast as possible, in a few milliseconds": one indexed lookup by domain, no DNS queries, no other network calls.
- `interval` and `burst` are **not recommended**; the docs say to remove them. `ask` (or a `permission` module) is the only supported abuse control, and it is global, not per site.
- Caddy's own throttle is 10 ACME attempts per account per 10 seconds, and it falls back from Let's Encrypt to ZeroSSL on failure. Source: https://caddyserver.com/docs/automatic-https

### Recommended shape

```
{
	email ops@example.com
	on_demand_tls {
		ask http://127.0.0.1:4000/internal/tls-ask
	}
	storage <shared module>   # only when running more than one instance
}

*.sites.example.com {
	tls {
		dns <provider> {env.DNS_API_TOKEN}
	}
	reverse_proxy web:3000
}

https:// {
	tls {
		on_demand
	}
	reverse_proxy web:3000
}
```

- **[V]** The wildcard needs the DNS challenge, which needs a custom Caddy build with the DNS provider plugin. Subdomains then use the wildcard cert and get no individual certs. Source: https://caddyserver.com/docs/caddyfile/patterns#wildcard-certificates
- **[V]** The `https://` catch-all with `on_demand` is the documented on-demand pattern.
- **[U]** The combined file above is assembled from the two documented patterns and has not been run.

### Storage across instances

- **[V]** Instances configured with the same storage share certificates and coordinate issuance as a cluster. The default is the local file system. Source: https://caddyserver.com/docs/automatic-https#storage
- **[U]** Shared storage needs a plugin (Redis, Consul, DynamoDB and others are listed at https://github.com/caddyserver/certmagic/wiki/Storage-Implementations); the Caddy project endorses none. Community reports say S3 modules lack reliable locking. A single instance with a persistent volume needs none of this.

### Pitfalls

- The ask endpoint should answer yes only for domains in our custom-domain table that we have already verified point at us. Verify DNS when the customer adds the domain and store the result; do not check DNS inside `ask`.
- Keep the ask endpoint off the public internet (it reveals which domains are customers).
- Let's Encrypt limits **[V]** (page updated 2026-08-05, https://letsencrypt.org/docs/rate-limits/):
  - 300 new orders per account per 3 hours. This is the one a burst of new customer domains can hit.
  - 50 certificates per registered domain per 7 days. Each customer domain is its own registered domain, so this rarely binds; the wildcard is one cert.
  - 5 certificates per exact set of identifiers per 7 days. Losing storage and re-issuing repeatedly hits this, so the volume must persist.
  - 5 authorization failures per identifier per account per hour. This is why `ask` must reject domains whose DNS is not ready.
  - ARI-coordinated renewals are exempt from all limits.
- The first visitor to a new domain waits for issuance (a few seconds).
- Use the Let's Encrypt staging CA (`acme_ca`) while testing.

### Managed alternative: Cloudflare for SaaS **[V]**

Source: https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/

| | Free / Pro / Business | Enterprise |
|---|---|---|
| Hostnames included | 100 | custom |
| Max hostnames | 50,000 | unlimited |
| Each additional hostname | $0.10 | custom |
| Wildcard custom hostnames | no | yes |
| Apex proxying | no | paid add-on |
| Custom (uploaded) certificates | no | yes |

- **[U]** The $0.10 is per hostname per month; the table does not state the period.
- Setup is a fallback origin plus a custom-hostname API call per customer domain; the customer adds a CNAME. Source: https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/getting-started/
- Without Enterprise, customers cannot point an apex domain (A record) at us; only CNAMEs such as `www`. Caddy on a fixed IP has no such limit.

## 3. Next.js 16: Cache-Control from `proxy.ts`

**Confirmed from the installed Next 16.4.6 source; the docs do not state it.**

- **[V]** In production, a `Cache-Control` already on the response is kept for dynamically rendered pages. `next/dist/build/templates/app-page-runtime.js` (around line 1306) only applies its default when `!res.getHeader('Cache-Control')`, and `next/dist/server/send-payload.js` (line 60) has the same guard with the comment "If cache control is already set on the response we don't override it to allow users to customize it via next.config".
- **[V]** In development the same file runs `res.setHeader('Cache-Control', 'no-store')` unconditionally (line 1235, "Documents and RSC payloads must not be stored in development"). That explains `no-store` in dev and `max-age=60` in prod.
- **[V]** Draft mode forces `private, no-cache, no-store, …` regardless.
- **[V]** The documented default for dynamic pages is `private, no-cache, no-store, max-age=0, must-revalidate`. Source: https://nextjs.org/docs/app/guides/self-hosting#automatic-caching
- **[U]** Because this is an implementation detail, a minor release could change it. Our current `proxy.ts` sets `public, max-age=60` on rewrites; an e2e assertion against `next start` is the guard. I did not check whether one exists, and I did not run the server today.

### Rendering a route per request

- **[V]** `cacheComponents` is not enabled in `apps/web/next.config.ts`, so `export const dynamic = 'force-dynamic'` works today. That export is removed when Cache Components is enabled. Source: https://nextjs.org/docs/app/api-reference/file-conventions/route-segment-config
- **[V]** `await connection()` from `next/server` opts a route into request-time rendering in both models, as does reading `cookies()`, `headers()` or `searchParams`. Source: https://nextjs.org/docs/app/guides/self-hosting#environment-variables
- Suggested: `await connection()`, since it survives a later switch to Cache Components.
