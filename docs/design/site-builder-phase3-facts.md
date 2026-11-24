# Site builder Phase 3: technical fact check

Checked 2026-11-24. Every claim carries a source URL, or one of these tags:

- **[local]**: measured or tested in this repo on the check date.
- **[unverified]**: from memory or third-party pages, not confirmed against a primary source.

## Findings that change the plan

1. **`next/og` cannot render Indic text correctly in the installed Next.js 16.4.6.** A test render broke conjuncts and matra order in all six scripts that have a fallback font, and printed empty boxes for Gujarati, Gurmukhi and Odia (section 4).
2. **Apex domains on Cloudflare for SaaS are Enterprise-only** (paid add-on). On Free, Pro and Business, customers can only connect a subdomain such as `www`, unless their DNS provider flattens CNAMEs at the root.
3. **Safe Browsing v4 is non-commercial only.** Use the Web Risk Lookup API: 100,000 calls a month free, then $0.50 per 1,000.
4. **Bhashini's public APIs are for proof of concept only.** Production use needs a paid agreement with no published price.

## 1. Cloudflare for SaaS (custom hostnames)

### API

All paths are under `https://api.cloudflare.com/client/v4`.

| Action | Call | Token permission |
|---|---|---|
| Create | `POST /zones/{zone_id}/custom_hostnames` | SSL and Certificates Write |
| Get | `GET /zones/{zone_id}/custom_hostnames/{id}` | SSL and Certificates Read or Write |
| Delete | `DELETE /zones/{zone_id}/custom_hostnames/{id}` | Write **[unverified]**, page not fetched |
| Set fallback origin | `PUT /zones/{zone_id}/custom_hostnames/fallback_origin`, body `{origin}` | SSL and Certificates Write |

Sources: [create](https://developers.cloudflare.com/api/resources/custom_hostnames/methods/create/), [get](https://developers.cloudflare.com/api/resources/custom_hostnames/methods/get/), [fallback origin](https://developers.cloudflare.com/api/resources/custom_hostnames/subresources/fallback_origin/methods/update/).

- **Narrowest token:** one zone-scoped token with "SSL and Certificates: Edit" on the SaaS zone only. The fallback DNS record is a one-time dashboard step, so the app token needs no DNS permission.
- **Create body:** `hostname` (required), and `ssl: {method: "http" | "txt" | "email", type: "dv"}`. `http` is recommended once the CNAME is in place.
- **Hostname status (`result.status`):** `pending`, `active`, `active_redeploying`, `moved`, `pending_deletion`, `deleted`, `pending_blocked`, `pending_migration`, `pending_provisioned`, `provisioned`, `blocked`, plus `test_*` values.
- **Certificate status (`result.ssl.status`):** `initializing`, `pending_validation`, `pending_issuance`, `pending_deployment`, `active`, `expired`, `deleted`, several `*_timed_out` values, and others. Errors are in `ssl.validation_errors`.
- **Ownership fields:** `ownership_verification` (`type: txt`, `name`, `value`) and `ownership_verification_http` (`http_url`, `http_body`). Errors are in `verification_errors[]`.
- **Ready means both:** a hostname is live only when `status` and `ssl.status` are both `active` and its DNS points at the CNAME target ([getting started](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/getting-started/)).
- **Retries:** Cloudflare retries validation 75 times over seven days, then deletes the hostname ([backoff schedule](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/domain-support/hostname-validation/backoff-schedule/)). The app must handle a hostname that disappears.

### Setup, plans and limits

- **Fallback origin:** a proxied `A`, `AAAA` or `CNAME` record pointing at the origin, then set as fallback origin. Add a proxied CNAME target (for example `customers.example.com`) for customers to point at ([getting started](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/getting-started/)).
- **Pricing:** Free, Pro and Business each include 100 hostnames, then $0.10 per extra hostname, up to 50,000 ([plans](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/)).
- **Billing period:** not stated in the docs. The launch blog describes monthly billing ([blog](https://blog.cloudflare.com/cloudflare-for-saas-for-all-now-generally-available/)). **[unverified]** for current invoices.
- **What counts:** every hostname counts until deleted, including pending ones, so failed setups must be deleted. Past the quota, the API rejects creates on non-Enterprise plans ([quotas and billing](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/quotas-and-billing/)).
- **Apex domains:** apex proxying is an Enterprise paid add-on using static IPs ([apex proxying](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/advanced-settings/apex-proxying/), [plans](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/)). Wildcard custom hostnames and custom certificates are also Enterprise-only.
- **Rate limit:** 1,200 API requests per five minutes per user or token, and 200 requests a second per IP. Exceeding it blocks all calls for five minutes with HTTP 429 ([limits](https://developers.cloudflare.com/fundamentals/api/reference/limits/)). Poll status with backoff, not per page view.

### With a Next.js app on a VPS

This is the natural fit. The proxied fallback record points at the VPS, Cloudflare terminates TLS for each customer hostname, and the app picks the tenant from the `Host` header.

- **[unverified]** The origin receives the customer's hostname as `Host` by default.
- **[unverified]** Which origin certificate Full (strict) mode accepts for custom hostnames. Test this before launch.

### With Vercel

Do not combine the two. Vercel has its own multi-tenant domain API: add a domain to the project, verify it, and Vercel issues the certificate ([Vercel multi-tenant domains](https://vercel.com/docs/multi-tenant/domain-management)).

Vercel recommends against a reverse proxy in front of it: its bot protection stops working, caches can go stale, and certificate challenges need exemptions ([Vercel guide](https://vercel.com/kb/guide/cloudflare-with-vercel)).

- **[unverified]** Vercel's per-project domain limits and apex record values were not checked.

## 2. Google Safe Browsing

| | Safe Browsing Lookup v4 | Web Risk Lookup | Web Risk Update |
|---|---|---|---|
| Commercial use | No | Yes | Yes |
| How it works | Send URLs to Google | Send one URL per call (`uris.search`) | Keep a local hash database, confirm matches with `hashes.search` |
| Price | Free | 100,000 calls a month free, then $0.50 per 1,000 | Database sync free, confirmations $50 per 1,000 |

- **Terms:** Safe Browsing is for "non-commercial use only", meaning not for sale or revenue-generating purposes, and Google points commercial users to Web Risk ([usage limits](https://developers.google.com/safe-browsing/v4/usage-limits), [terms](https://developers.google.com/safe-browsing/terms)).
- **Safe Browsing limits:** up to 500 URLs per request; quota is a per-project default shown in the console, with no published number ([Lookup API](https://developers.google.com/safe-browsing/v4/lookup-api)).
- **Web Risk pricing:** [pricing page](https://cloud.google.com/web-risk/pricing). Calling the Update API's `computeDiff` moves your lookups to the $50 rate, so do not mix the two.
- **What a hit means:** the URL is on one of Google's threat lists (malware, social engineering, unwanted software) at that moment. An empty response means no match, not proof of safety. Each match has a `cacheDuration` for how long to treat it as unsafe.
- **Wording rules:** warnings must say "suspected" or "potentially", and carry "Advisory provided by Google" ([usage limits](https://developers.google.com/safe-browsing/v4/usage-limits)). **[unverified]** whether Web Risk has the same wording rules.

**Recommendation:** Web Risk Lookup API. Scan outbound links when a site is published and re-scan on a schedule, checking each distinct URL once. The free 100,000 calls should cover early volume, and it needs no local database.

## 3. Indic scripts on the web

### System fonts

- **Windows:** Nirmala UI ships since Windows 8 and covers Devanagari, Bengali, Gujarati, Gurmukhi, Kannada, Malayalam, Odia, Tamil and Telugu ([Wikipedia](https://en.wikipedia.org/wiki/Nirmala_UI), [Microsoft](https://learn.microsoft.com/en-us/globalization/fonts-layout/font-support)).
- **Android:** **[unverified]** ships a Noto Sans font per script; coverage on old or low-cost devices varies.
- **iOS and macOS:** **[unverified]** ship Kohinoor (Devanagari, Bangla, Telugu, Gujarati) and Sangam MN (Tamil, Kannada, Malayalam, Odia, Gurmukhi) families.

All nine scripts should render with a plain `system-ui, sans-serif` stack on current systems, but the look differs by platform and bold weights are not always present. Self-host only if a consistent brand look matters.

### Self-hosting Noto

One family per script: Noto Sans Devanagari, Tamil, Telugu, Kannada, Malayalam, Bengali, Gujarati, Gurmukhi and Oriya (Odia). Licence is OFL-1.1, which allows commercial use and self-hosting (`@fontsource/noto-sans-devanagari` 5.3.0 package metadata, **[local]**).

Measured woff2 sizes for the script subset, from the fontsource packages on jsDelivr **[local]**:

| Script | Regular (400) | Bold (700) | Variable (all weights) |
|---|---|---|---|
| Devanagari | 49 KB | 53 KB | 118 KB |
| Bengali | 43 KB | 47 KB | 105 KB |
| Odia | 38 KB | 40 KB | 97 KB |
| Gujarati | 36 KB | 36 KB | 110 KB |
| Telugu | 35 KB | 37 KB | 121 KB |
| Kannada | 27 KB | 28 KB | 89 KB |
| Malayalam | 24 KB | 24 KB | 87 KB |
| Tamil | 14 KB | 15 KB | 49 KB |
| Gurmukhi | 11 KB | 11 KB | 33 KB |

Load only the script the site uses. Two static weights are smaller than the variable file.

### Typography pitfalls

- **Letter-spacing:** spacing must fall between syllables, not characters ([W3C Indic layout requirements](https://www.w3.org/TR/ilreq/)). Set `letter-spacing: 0` for Indic text; tracking on headings or uppercase labels breaks conjuncts.
- **Line breaks:** break at word boundaries, or at orthographic syllables when a word must split (same source). Truncating by character count can cut a syllable in half.
- **First-letter styling:** applies to the whole syllable, not the first code point (same source).
- **Line-height:** **[unverified]** the W3C document gives no number. Stacked conjuncts and marks above and below need more room than Latin; common practice is about 1.6 to 1.8 for body text, and no `overflow: hidden` on tight headings.
- **No case:** `text-transform: uppercase` does nothing, and italics are usually synthesised.

## 4. Next.js 16 `ImageResponse` for OG images

- **Availability:** `next` is 16.4.6 in `apps/web/package.json`. `ImageResponse` is imported from `next/og` and bundles `@vercel/og` 1.0.1 with satori 0.29.0 **[local]**.
- **Runtime:** the bundle has both Node.js and Edge builds **[local]**, and the docs example reads fonts with `node:fs` ([docs](https://nextjs.org/docs/app/api-reference/functions/image-response)). It works on a VPS in the Node.js runtime.
- **Limits:** flexbox only (no grid), 500 KB bundle limit including fonts and images, fonts as `ttf`, `otf` or `woff`, default size 1200 by 630 (same docs page).
- **Speed:** the nine-line test image took 1.9 s on first render including font downloads **[local]**. Cache the output.

### Indic text does not work

Test: rendered one line per script through the bundled `ImageResponse`, with no custom fonts **[local]**.

- **Devanagari, Tamil, Telugu, Kannada, Malayalam, Bengali:** glyphs appear, but shaping is wrong. Conjuncts are not formed (visible viramas) and vowel signs are in the wrong position. The text reads as broken to a native reader.
- **Gujarati, Gurmukhi, Odia:** empty boxes. The bundle has no automatic fallback font for them.
- **Cause:** the bundled build contains no HarfBuzz shaping engine **[local]**. The upstream satori README now says it uses HarfBuzz ([satori](https://github.com/vercel/satori)); **[unverified]** which release added it, and whether it fixes Indic.

Options, cheapest first:

1. Keep text out of the generated image: use the business photo or logo, and let `og:title` carry the Indic text. WhatsApp shows the title next to the image.
2. Render the card with a headless browser screenshot at publish time and store the PNG.
3. **[unverified]** Try a newer standalone satori and repeat the test above before relying on it.

### Platform image requirements

| Platform | Size | File limit | Notes |
|---|---|---|---|
| WhatsApp | at least 300 px wide, aspect ratio 4:1 or less | under 600 KB | `og:image` must be an absolute URL; tags within the first 300 KB of HTML ([docs](https://developers.facebook.com/documentation/business-messaging/whatsapp/link-previews/)) |
| Facebook | 1200 by 630 recommended, 600 by 315 for large cards, 200 by 200 minimum, about 1.91:1 | 8 MB | set `og:image:width` and `og:image:height` so the first share shows the image ([docs](https://developers.facebook.com/docs/sharing/webmasters/images/)) |
| X | **[unverified]** 2:1, at least 300 by 157 | under 5 MB; JPG, PNG, WEBP, GIF ([cards markup](https://developer.x.com/cards/markup)) | cache duration **[unverified]** |

- **One image for all three:** 1200 by 630, kept under 600 KB. Third-party reports say previews fail above about 300 KB on WhatsApp, so aim lower **[unverified]**.
- **Caching:** Facebook caches by image URL, so a changed image needs a new URL (same Facebook source). Put a content hash or version in the image URL. WhatsApp's caching is not documented.

## 5. Machine translation for Indian languages

| Option | Price | Commercial use | Notes |
|---|---|---|---|
| Claude Haiku 5.5 (`claude-haiku-5-5`) | $0.10 in, $0.50 out per million tokens | Yes | Batch API is half price |
| Claude Sonnet 5.5 (`claude-sonnet-5-5`) | $2 in, $10 out per million tokens | Yes | |
| Claude Opus 5.5 (`claude-opus-5-5`) | $4 in, $20 out per million tokens | Yes | |
| Google Cloud Translation (NMT) | $20 per million characters, first 500,000 a month free | Yes | Translation LLM: $10 in plus $10 out per million characters ([pricing](https://cloud.google.com/translate/pricing)) |
| Sarvam Translate v1 / Mayura v1 | ₹20 per 10,000 input characters | Yes, paid API | ([pricing](https://docs.sarvam.ai/api/getting-started/pricing)) |
| Bhashini (government) | Not published | Not without a paid agreement | ([docs](https://bhashini.gitbook.io/bhashini-apis)) |

- **Claude:** prices are from the Claude API reference bundled with Claude Code, cached 2026-11-03; confirm on the live pricing page. The translation prompt can carry tone, brand terms and length limits. **[unverified]** quality on Odia and Punjabi; no benchmark was checked.
- **Sarvam:** `sarvam-translate:v1` covers English plus all 22 scheduled Indian languages, formal style only, 2,000 characters per request. Mayura handles colloquial and code-mixed styles ([model page](https://docs.sarvam.ai/api-reference-docs/getting-started/models/sarvam-translate)). Rate limits and the open-weights licence were not found **[unverified]**.
- **Bhashini:** "Usage of these APIs shall be for the purposes of PoC only"; production use or charging end users means contacting the Bhashini team for the paid version (docs link above). `bhashini.ai` is reported to be a separate private vendor **[unverified]**.
- **Quality:** no independent comparison was checked. Any quality ranking here would be **[unverified]**; run a small native-speaker review per language before choosing.

**Recommendation:** use Claude, already in the stack, for short website copy, since the prompt can hold tone and the business's own terms. Keep Sarvam as the fallback to evaluate if reviewers reject output in a specific language. Rule out Bhashini for launch.

At ₹20 per 10,000 characters, Sarvam costs ₹2,000 per million characters, close to Google's $20. For short copy, cost will not decide between any of these.

## Not checked

- Cloudflare delete-endpoint permission, origin `Host` header and Full (strict) behaviour for custom hostnames.
- Vercel domain limits and apex records.
- X card dimensions and cache duration from a primary source (the X docs page returned HTTP 402).
- Android and Apple system font lists from primary sources.
- Translation quality for any provider.
