# Website builder: more and better templates (25 Nov 2026)

Source: Mobbin site sections and screens viewed on 24 and 25 Nov 2026, plus a read of `catalog.json`, `blocks.tsx`, `lib/sites.ts`, `templates.go` and the five current templates. Every link is a section or screen that was actually viewed. Items marked **(inferred)** are our own judgement.

Three limits of the research:
- Mobbin is mostly software and brand sites. Real small-business sites were found for cafés (Monte, Little Amps, Escape Cafe), studios (metic., Fiasco, Freshman) and a few others. For garages, kirana shops, tuition centres, CAs, builders and NGOs the references are the closest good-looking sites in the same trade, not Indian local businesses.
- Mobbin section captures are desktop width. Everything said about phones is **(inferred)** from the captures plus how our renderer stacks.
- Nothing here was rendered. Palettes were checked by calculation only (section 2).

## 0. Why the five current templates look alike

Found in the code, not on Mobbin:
1. No template sets a block `variant`. All five open with the same centred, text-only hero, so the picker shows one layout in five colours.
2. `font_heading` is in the catalog and Tadka sets it to `serif`, but nothing reads it. `themeStyle` in `lib/sites.ts` never emits a heading font and no heading class uses one.
3. If the company profile has an `email_accent`, `fillTemplate` overwrites every template's `primary` with it, so all cards share one accent.
4. Templates cannot ship photos (an image is an uploaded asset id), so every thumbnail is text only.
5. `rounded` resolves to `ui-rounded` on Apple devices only. On Android and Windows it falls back to the normal system sans unless Nunito is installed.

Points 1 and 2 are fixed by the templates in section 2 and gap 1 in section 3.

## 1. What the references do

### Style families

| Family | References | What makes it look professional |
|---|---|---|
| Minimal | [metic. services list](https://mobbin.com/sites/sections/e738d0c3-19b6-4be1-9c5c-b215c139c635), [SIGMA product lines](https://mobbin.com/sites/sections/c18bb82d-1622-4e93-8bbb-2986150a1fec), [Titan service lists](https://mobbin.com/sites/sections/223cc7db-df8b-4f0c-89b5-fa1dd9b4569d), [Freshman portfolio hero](https://mobbin.com/sites/sections/cc46fe95-d2fb-46a6-927a-96c9bc6d5ecc) | Off-white page, near-black text, no accent colour at all or one small dot of it. Hairline dividers instead of cards. Very large headings, small body text, a lot of empty space. |
| Bold | [Patch](https://mobbin.com/sites/sections/07c4ed44-77b5-432c-b087-ce16f7e9931d), [Kajabi](https://mobbin.com/sites/sections/3f75968b-e9c4-4f47-aec4-5233e5aa404e), [BLOK on Framer](https://mobbin.com/sites/sections/933f929a-d118-4b5d-834d-c60a0f488cae), [Atlas](https://mobbin.com/sites/sections/2f379c3d-dfcb-4eb2-8a2f-d01cc298f5f5), [Cloudstudio](https://mobbin.com/sites/sections/bd08f963-ed5b-425f-b282-a6040e95fff1) | Heavy, often uppercase headline of three or four short lines. One saturated colour or a dark page. Square corners. One button. |
| Warm / editorial | [Ease](https://mobbin.com/sites/sections/6695790f-7142-4e99-86a9-2632963b795d), [Amigo](https://mobbin.com/sites/sections/dcd77131-0901-4971-929e-00773c34a5f3), [Monte](https://mobbin.com/sites/sections/664a16ca-816d-495d-b577-e08ce5c6f1a5), [Fresha](https://mobbin.com/sites/sections/f365cff8-e8ea-4525-af1d-d87ee93f0bb3), [Little Amps footer](https://mobbin.com/sites/sections/9ee9d990-cbe1-40fe-b2ca-03ae743e3f0a) | Serif headings over sans body. Cream page, never pure white. One earthy colour (deep green, terracotta). Real photos with one corner radius. |
| Corporate | [Complex Law](https://mobbin.com/sites/sections/a3c3f82e-5230-4d24-85c2-15afdcce31cc), [Legora numbers](https://mobbin.com/sites/sections/2cf16054-0dd8-4dcc-8358-a8fbb6dad125), [OpenTable](https://mobbin.com/sites/sections/4367b2d2-ebca-41fe-a857-eaee7eac1f9d), [Kajabi split hero](https://mobbin.com/sites/sections/c05626d4-008e-4977-abf1-052cb7cfed46) | White page, navy or blue accent, text left and photo right, three equal cards, two buttons (filled and outline). |
| Playful | [Maxima Therapy](https://mobbin.com/sites/sections/ffe7da9b-558b-4650-ac7e-7bd4580ce88a), [Fiasco](https://mobbin.com/sites/sections/814e0a13-81d8-4d89-95f1-33b72ef0d9d3), [Going](https://mobbin.com/sites/sections/0334bd79-fc3c-4e45-b7dd-b1f05f5b893d), [Wix Learn](https://mobbin.com/sites/sections/f32df1ca-d4e1-44b3-9a10-5c887acbaaff) | Large corner radius, pill buttons, two or three flat bright colours on cream, rounded type, illustration in place of photos. |

**Colour and type with system fonts (inferred from the above).** Premium comes from restraint, not from a webfont: an off-white background, one accent used only for buttons and prices, serif headings (`ui-serif` is New York on Apple, Georgia elsewhere) over sans body, and a radius that matches the family (0 for bold and minimal, 16px for playful). All of this is reachable with today's tokens once the heading font is wired.

### Verticals

| Vertical | References | What to copy |
|---|---|---|
| Clinic, doctor, dentist | [Ease hero](https://mobbin.com/sites/sections/6695790f-7142-4e99-86a9-2632963b795d), [Amigo hero](https://mobbin.com/sites/sections/dcd77131-0901-4971-929e-00773c34a5f3), [Hims hero](https://mobbin.com/sites/sections/2aac84d2-0606-4ed6-a558-71871c6ee181) | Calm serif headline, one line of body, one "Book" pill repeated in the header. A photo of people, not equipment. Order: hero, treatments, doctor, hours, FAQ, form. |
| Salon, spa, beauty | [Fresha hero](https://mobbin.com/sites/sections/f365cff8-e8ea-4525-af1d-d87ee93f0bb3), [Fresha venue cards](https://mobbin.com/sites/sections/62605a5b-b047-4feb-aa94-a60be59a6136), [metic. price rows](https://mobbin.com/sites/sections/e738d0c3-19b6-4be1-9c5c-b215c139c635) | Interior photos sell the place. The service list is rows of "name … from price", not cards. Price list sits directly under the hero. |
| Restaurant, café, cloud kitchen | [Monte hero](https://mobbin.com/sites/sections/664a16ca-816d-495d-b577-e08ce5c6f1a5), [Monte menu](https://mobbin.com/sites/sections/9cea9d4c-5557-44cf-97e4-41d7d086df12), [Corgi menu](https://mobbin.com/sites/sections/9df7bc10-f0b2-4ad1-99b1-2dbab807ab6b), [sweetgreen dishes](https://mobbin.com/sites/sections/ef00c77c-841c-4e12-b207-df44ce97a57a), [Monte footer](https://mobbin.com/sites/sections/6ecba422-fe26-482b-8225-8e24ec959d84) | Header is three things: Menu, name, Book a table. Menu is grouped price lists with a small description line. "Open today till…" near the top. Footer is contact, hours, find us. |
| Retail, kirana, boutique | [Loom shop](https://mobbin.com/sites/sections/095d014e-3d91-4684-b137-1c26ac79b120), [Escape Cafe products](https://mobbin.com/sites/sections/c2bdfec6-82d4-4e22-baaa-5f866e5c7cdf), [Assembly Coffee](https://mobbin.com/sites/sections/a4f80396-d25b-470b-a707-f6a0e25283e5) | One plain sentence about the shop, then products straight away. Square product photos on one flat background, name and price under each. |
| Manufacturer, trader, wholesaler | [Farm Minerals](https://mobbin.com/sites/sections/8000e65f-fa00-4225-ae66-f35575dab264), [SIGMA](https://mobbin.com/sites/sections/c18bb82d-1622-4e93-8bbb-2986150a1fec), [Atlas](https://mobbin.com/sites/sections/2f379c3d-dfcb-4eb2-8a2f-d01cc298f5f5) | Three product lines, each a photo, a name and two lines. "Contact us" is the only header action. Close-up factory photos carry the dark version. |
| Coaching, tuition | [Kajabi](https://mobbin.com/sites/sections/c05626d4-008e-4977-abf1-052cb7cfed46), [Teachable](https://mobbin.com/sites/sections/89547abb-69ec-4ab3-802b-5a1eb28569c4), [Wix Learn](https://mobbin.com/sites/sections/f32df1ca-d4e1-44b3-9a10-5c887acbaaff) | Split hero with the teacher's photo. Courses as coloured cards. Two buttons: join and "see demo". |
| CA, legal, consultant | [Complex Law](https://mobbin.com/sites/sections/a3c3f82e-5230-4d24-85c2-15afdcce31cc), [Titan](https://mobbin.com/sites/sections/223cc7db-df8b-4f0c-89b5-fa1dd9b4569d), [YLLW services](https://mobbin.com/sites/sections/c838e6c7-2e50-40c5-8b29-f61445fd5bc0) | Serif heading, three practice areas, then a plain divided list of everything else. Quiet colours. "Who we serve" is its own list. |
| Real estate, builder | [Samara](https://mobbin.com/sites/sections/19d48f9c-20f5-4d4e-84a8-a0abfcf2133c), [YLLW photo with card](https://mobbin.com/sites/sections/acd36b4c-2fd8-42eb-a9e0-abc8910591eb) | Full-width property photo with very little text over it. A white card on the photo holds the call to action. Weak coverage on Mobbin. |
| Gym, fitness, yoga | [BLOK](https://mobbin.com/sites/sections/933f929a-d118-4b5d-834d-c60a0f488cae), [Klipsan on Squarespace](https://mobbin.com/sites/sections/88798f15-ebe6-4c02-9864-9f2d7b3190a1), [Oku membership](https://mobbin.com/sites/sections/f66ba39e-9d4f-4336-955d-8721fef92d47), [Fruitful single plan](https://mobbin.com/sites/sections/a3608df7-8dd4-4bb5-a149-ce8276e15175) | Uppercase three-line headline, square corners, an action photo. Membership is two or three cards with one line each; a single plan is one centred card. |
| Interior, architect, photographer, events | [Freshman](https://mobbin.com/sites/sections/cc46fe95-d2fb-46a6-927a-96c9bc6d5ecc), [photographer grid on Squarespace](https://mobbin.com/sites/sections/b32fe6a7-0a8f-4d6e-b438-f7c86a6fa69b), [Eventbrite](https://mobbin.com/sites/sections/461338aa-4933-442c-884a-6cc590e932bf) | The work is the page. Name, one sentence, then a tight photo grid before any text section. Almost no colour. |
| Travel agency | [KOBU destination](https://mobbin.com/sites/sections/5dd15202-c09f-4df9-bee1-204a6b3d71af), [Tripadvisor regions](https://mobbin.com/sites/sections/e6ba6939-d775-415b-9688-45b15baa52c1), [Going traveller quote](https://mobbin.com/sites/sections/2d472cc1-6985-4c03-9199-319659f1cd44) | Destination photo cards with the place name on the photo. Testimonial is a traveller's own photo beside a short quote. |
| Auto repair, local services | [Airtasker](https://mobbin.com/sites/sections/2c48bf0b-3622-44fc-8fb1-76ff63d17cbb), [Square](https://mobbin.com/sites/sections/29337e63-1f63-4b05-bdfd-672cd2a1f5ed) | Photo of the work, a checklist of jobs done, a row of five plain reassurances with icons under the hero. No garage site was found. |
| NGO, trust | [Maxima Therapy](https://mobbin.com/sites/sections/ffe7da9b-558b-4650-ac7e-7bd4580ce88a), [PayPal nonprofits](https://mobbin.com/sites/sections/baa86742-e77d-41df-8c61-d845c7b96d65) | "Donate" is the one coloured button in the header. A small "Our story" label above the headline. Warm cream page. |
| IT services, agency | [metic. services](https://mobbin.com/sites/sections/257ac093-d995-4565-8574-f0b9ff580b04), [metic. packages](https://mobbin.com/sites/sections/88e09323-56f5-4fc0-bc33-3d213c2a974b), [Koto](https://mobbin.com/sites/sections/968754c4-c525-4315-a823-07721be58b1b) | Numbered service rows with "from price" and duration. Four fixed-price packages, one in black. Plain language ("You talk to the person who builds"). |

### Shared sections

- **Services with photos**: [Square photo rows](https://mobbin.com/sites/sections/54ba196c-c979-4564-a54a-c02a23e8e8f7), [Wise photo cards](https://mobbin.com/sites/sections/30aeab8b-e5a9-4677-ac01-d547a6de6dcd). The search for a "bento" grid returned these rows and cards, not a bento; small sites use photo, title, two lines.
- **Numbers**: [Legora](https://mobbin.com/sites/sections/2cf16054-0dd8-4dcc-8358-a8fbb6dad125), [Amigo](https://mobbin.com/sites/sections/1a3250eb-e77f-4fb9-aa57-12eec3869d08), [Mixpanel](https://mobbin.com/sites/sections/78a46ec2-49e2-45e9-a0bd-33d65bc69ff2). Three numbers, huge, one short label each.
- **Testimonials**: [Customer.io](https://mobbin.com/sites/sections/97fbd41c-4d64-4197-930b-f2a24fc65289) (three across, small photo), [Eventbrite](https://mobbin.com/sites/sections/011f833e-b660-43b2-abce-affa60297789) (one large quote on a dark band).
- **Logos**: [Legora](https://mobbin.com/sites/sections/466b730c-eb87-4806-aa4a-15ffe507d2e2), [Daydream](https://mobbin.com/sites/sections/b8732dce-2387-4f3b-9cb5-925d6b80d54d). One grey row under the hero.
- **Phone (inferred)**: every reference collapses to one column in the order text, button, photo. The sites that would survive a phone best are the ones with one button and short headlines. Our renderer already does this (full-width buttons under 768px, snap-scroll testimonials).

## 2. Proposed templates (15)

The five existing keys stay, with variants set and two theme fixes. Ten are new. Palettes, names and orders are **(inferred)**. Contrast is text on background, calculated with the same formula as `catalog.go`; every accent is also at least 4.5 on its background because Services prints prices in the accent colour. Radius: none 0, sm 4px, md 8px, lg 16px. Font is written body / heading; a heading font only takes effect after gap 1.

| # | Key · Name | Vertical | Style | primary / background / text | Contrast | Font | Radius |
|---|---|---|---|---|---|---|---|
| 1 | `services` · Trusty | Plumber, electrician, garage, repairs | Corporate | `#1d4ed8` / `#ffffff` / `#0f172a` | 17.9 | sans | md |
| 2 | `shop` · Bazaar | Retail, kirana, boutique, sweets | Playful | `#c2410c` / `#fffbf5` / `#1c1917` | 17.0 | rounded | lg |
| 3 | `clinic` · Care | Clinic, dentist, physio, lab | Warm | `#0f766e` / `#f8fafa` / `#11302d` | 13.5 | sans / serif | md |
| 4 | `restaurant` · Tadka | Restaurant, caterer, tiffin | Warm | `#b4532a` / `#faf3e7` / `#2b1a12` | 15.1 | sans / serif | sm |
| 5 | `trader` · Works | Manufacturer, trader, wholesaler | Bold | `#b45309` / `#ffffff` / `#111827` | 17.7 | sans | none |
| 6 | `salon` · Glow | Salon, spa, beauty, bridal | Warm | `#9d174d` / `#fdf6f3` / `#3b1f2b` | 13.9 | sans / serif | lg |
| 7 | `cafe` · Brew | Café, bakery, cloud kitchen | Playful | `#14532d` / `#f4f1e6` / `#14352a` | 11.8 | rounded | lg |
| 8 | `coaching` · Scholar | Coaching, tuition, classes | Playful | `#4338ca` / `#ffffff` / `#1e1b4b` | 16.0 | rounded | lg |
| 9 | `professional` · Ledger | CA, lawyer, consultant | Corporate | `#1e3a5f` / `#fbfaf7` / `#1c2430` | 15.0 | sans / serif | sm |
| 10 | `realestate` · Estate | Builder, broker | Bold | `#7c5a1e` / `#faf8f5` / `#1c1917` | 16.5 | sans | none |
| 11 | `fitness` · Pulse | Gym, fitness, yoga | Bold, dark | `#a3e635` / `#0a0a0a` / `#fafafa` | 19.0 | sans | none |
| 12 | `studio` · Studio | Interior, architect, photographer, event planner | Minimal | `#1c1917` / `#fafaf9` / `#1c1917` | 16.7 | sans / serif | none |
| 13 | `travel` · Voyage | Travel agency, tours | Playful | `#0369a1` / `#f5fbff` / `#0c2d48` | 13.6 | rounded | lg |
| 14 | `ngo` · Seva | NGO, trust, community group | Warm | `#047857` / `#fffbeb` / `#1f2937` | 14.2 | rounded | lg |
| 15 | `agency` · Pixel | IT services, web or marketing agency | Minimal | `#b91c1c` / `#f3f2ef` / `#111111` | 16.9 | sans | sm |

Works changes its accent from `#f59e0b`, which is 2.2 on white and so unreadable as price text. Pulse is the only dark template; `onColor` gives its lime buttons black text at 13.9.

**Section order.** Only existing blocks and variants. Every template ends with `Footer` and `WhatsAppButton`; the footer variant is given last.

| Name | Order (variant) | Picker pitch |
|---|---|---|
| Trusty | Header (left) · Hero (split) · Services (cards) · About (image-right) · Testimonials (cards) · FAQ (accordion) · CallToAction (banner) · ContactForm (side) · MapHours (side) · Footer (columns) | Clear and dependable, for trades that get called. |
| Bazaar | Header (left) · Hero (split) · Catalogue (grid, enquire both) · Gallery (strip) · About (image-left) · Testimonials (cards) · MapHours (side) · ContactForm (side) · Footer (columns) | Your shop's products up front, with prices from your items list. |
| Care | Header (left) · Hero (split) · Services (cards, "Treatments") · About (image-left, "Meet the doctor") · MapHours (side) · Testimonials (cards) · FAQ (accordion) · ContactForm (side) · Footer (columns) | Calm and clear, so patients find timings and book. |
| Tadka | Header (centered) · Hero (cover) · Pricing (list, "Menu") · Gallery (grid) · About (image-right) · Testimonials (cards) · MapHours (side) · CallToAction (card) · ContactForm (stacked, "Reservations") · Footer (simple) | A warm menu-first page for places people sit down to eat. |
| Works | Header (left) · Hero (split) · Catalogue (list, enquire form) · Services (list, "What we make") · About (image-right) · Gallery (grid) · FAQ (open) · ContactForm (side, "Get a quote") · MapHours (side) · Footer (columns) | Straight to products and a quote form, for B2B buyers. |
| Glow | Header (centered) · Hero (cover) · Pricing (list, "Services and prices") · Gallery (strip) · About (image-left) · Testimonials (cards) · MapHours (side) · CallToAction (card) · ContactForm (stacked) · Footer (simple) | Soft and elegant, with your price list right under the photo. |
| Brew | Header (left) · Hero (split) · Pricing (cards, "Menu") · Gallery (grid) · About (stacked) · MapHours (side) · CallToAction (banner) · ContactForm (stacked, "Bulk orders") · Footer (columns) | Friendly and fresh, for cafés, bakeries and kitchens that deliver. |
| Scholar | Header (left) · Hero (split) · Services (cards, "Courses") · About (image-right, "Your teacher") · Pricing (cards, "Fees") · Testimonials (cards) · FAQ (accordion) · CallToAction (banner) · ContactForm (side) · MapHours (stacked) · Footer (columns) | Bright and encouraging, built around your courses and a demo class. |
| Ledger | Header (left) · Hero (centered) · Services (list) · About (image-left) · Text ("How we work") · FAQ (open) · ContactForm (side) · MapHours (side) · Footer (columns) | Quiet and serious, for firms that sell trust. |
| Estate | Header (left) · Hero (cover) · Gallery (grid, "Projects") · Services (cards) · About (image-right) · Testimonials (cards) · CallToAction (banner, "Book a site visit") · ContactForm (side) · MapHours (side) · Footer (columns) | Big photos first, then a site-visit request. |
| Pulse | Header (left) · Hero (cover) · Services (cards, "Classes") · Pricing (cards, "Membership") · Gallery (strip) · Testimonials (cards) · FAQ (accordion) · CallToAction (banner) · MapHours (side) · ContactForm (stacked) · Footer (simple) | Dark and high-energy, with classes and membership up top. |
| Studio | Header (centered) · Hero (centered) · Gallery (grid, "Selected work") · Services (list) · About (image-left) · Testimonials (cards) · CallToAction (card) · ContactForm (stacked) · Footer (simple) | Almost no decoration, so your work does the talking. |
| Voyage | Header (left) · Hero (cover) · Services (cards, "Trips") · Gallery (strip) · About (image-right) · Testimonials (cards) · FAQ (accordion) · CallToAction (banner) · ContactForm (side) · Footer (columns) | Bright and open, for trips people want to picture. |
| Seva | Header (left) · Hero (split) · About (stacked, "Our work") · Services (cards, "Programmes") · Gallery (grid) · Text ("How to help") · CallToAction (banner) · ContactForm (side) · MapHours (stacked) · Footer (columns) | Warm and plain-spoken, for causes that need people to join in. |
| Pixel | Header (left) · Hero (split) · Services (list) · Text ("How we work") · Gallery (grid, "Work") · Pricing (cards, "Packages") · Testimonials (cards) · FAQ (accordion) · CallToAction (card) · ContactForm (side) · Footer (simple) | Sharp and modern, with services listed like a rate card. |

Notes for the builder:
- `Testimonials` uses `cards` everywhere because `single` is not rendered (gap 6).
- A `cover` hero with no photo falls back to centred text, and a `split` hero to left-aligned text. Each template declares the variant that pays off once the owner adds a photo.
- Header: brand, at most four anchor links, and one button (`cta_label`) pointing at the ContactForm id. Four is the most that fits a phone without wrapping to three rows, since there is no mobile menu **(inferred)**.
- Bazaar and Works replace the old Services-as-products and empty Pricing with `Catalogue`, which shows real items and prices from invoicing and renders nothing when there are none.
- `templateKeys` in `templates.go` is a hard-coded list and needs the ten new keys.

**Placeholder copy rules**
1. Only the four existing slots are filled: `{{name}}`, `{{phone}}`, `{{email}}`, `{{address}}`.
2. Never invented, so shipped empty: `Testimonials.items`, `Pricing.items` (a price is required per item), `MapHours.hours`, `FAQ.items`, `Gallery.items`, every `price` in Services, and any number, year, rating, award, client name or "trusted by" line.
3. Text that must exist reads as an instruction starting with "Add your": Hero subtitle "Add your one-line description: what you do and where."; Services items "Add your first service", "Add your second service", "Add your third service" with empty descriptions; About body "Add your story: who you are, how long you have been here and what you are known for."
4. Section titles and button labels are real copy, because they name an action and claim nothing: "Our treatments", "Menu", "Book a visit", "Get a quote", "Find us".
5. No superlatives in any default ("best", "No. 1", "trusted", "leading").
6. The first chat turn should ask for the missing pieces in this order: one-line description, three services, a photo, hours **(inferred)**.

## 3. Gaps, ranked by impact

Not needed, checked against the code:
- **Dark theme.** The three colour tokens already allow it. Border, tint and muted are mixed from text and primary, buttons pick black or white by themselves, and the validator only asks for 4.5 between text and background. Pulse needs no new token. Untested in a browser.
- **Bento grid.** No small-business reference used one.
- **Logo strip.** Small owners rarely have client logos they may show. `Gallery (strip)` covers certificates and brand boards.

Worth building, in order:

| # | Gap | Why | Spec |
|---|---|---|---|
| 1 | Wire `font_heading` | Already in the catalog and set by Tadka, but ignored. It is the cheapest way to make templates look different, and four new templates rely on it. | `themeStyle`: emit `--site-font-heading` = `FONTS[t.font_heading]`, falling back to `--site-font`. Add `font-family: var(--site-font-heading)` to the `h1`, `h2` and `h3` classes in `blocks.tsx`. No schema change. |
| 2 | Hide empty list sections on the live site | `Services`, `Pricing` and `FAQ` with no items publish a lone heading. Honest templates ship these empty, so every new site would go live with orphan headings. | In each of the three, when `items` is empty: return the dashed `Empty` hint if `editing`, otherwise `null`. Same pattern `Gallery` and `Testimonials` already use. |
| 3 | Hero eyebrow and second button | Seen on Maxima, Kajabi, Square, Eventbrite. A small label ("Dentist in Kothrud") plus "Call" beside "Book" is the commonest hero in the references, and was in the Phase 1 spec. | New optional props: `eyebrow` (text, 40), `cta2_label` (text, 30), `cta2_href` (link). Eyebrow: 12px uppercase, 0.08em tracking, accent colour, above the `h1`. Second button uses the existing `ghostCls`, beside the first on desktop and under it on phone. |
| 4 | Photo on a service | sweetgreen, Wise, Square, Farm Minerals: photo cards are what separates a real business from a text page. | `Services.items[].image` (image, optional) and `image_alt` (text, 120). In `cards`: 16:10 photo on top of the card, clipped to the radius. In `list`: 96px square thumbnail on the left. No image means today's layout. |
| 5 | Numbers strip, owner-entered | Legora, Amigo, Mixpanel. Strong for Works, Ledger, Estate and Seva, but a magnet for invented figures. | New block `Stats`, max 1. `items` list (max 4): `value` (text, 12, required, `verbatim`), `label` (text, 40, required). Three or four across on desktop, two across on phone; value at `h2` size in the heading font, label small and muted. Renders nothing when empty. Not in any template by default. The assistant may add it only with figures the owner typed, the same rule as testimonial quotes. |
| 6 | `Testimonials` variant `single` | The catalog offers it and the renderer ignores it. Either build it or remove it. | One quote at a time, centred, 24px on phone and 32px on desktop in the heading font, name and role beneath, max width 720px. With more than one item, show the first only. If not built, delete the enum value. |
| 7 | Footer variant `band` | Monte and Escape Cafe end on a solid dark or accent band; the Phase 1 spec asked for one. It gives the page a clear end. | New enum value. Background `--site-text`, text `--site-bg`, same content and grid as `columns`. No new props. |
| 8 | Company accent overrides every template | Makes all picker cards one colour and is not checked against the background, so it can vanish on Pulse. | In `fillTemplate`, apply the accent only when its contrast with the template background is at least 4.5; otherwise keep the template's own colour. |

A sticky header with a phone menu (Phase 1 spec, not built) is left out on purpose: the four-link limit above avoids the need.

## 4. Template picker

Already in place (`start.tsx`): cards rendered live with the owner's business details, a Preview dialog with a desktop and phone toggle, and "Use this".

**References**
- [Kit](https://mobbin.com/screens/e68501f6-d531-44f4-95b7-17020742a3a2): category list beside the grid; Preview and Choose on each card.
- [Adobe Express](https://mobbin.com/screens/521817d3-4343-472d-a1ac-e599b4522835): one scrolling row of pill chips above the grid.
- [Relume](https://mobbin.com/screens/d2713135-29bf-42b9-a565-b24786ffc1c6): a "Business Type" filter that includes "Local Business", and a dead-end empty state when a filter matches nothing.
- [Webflow](https://mobbin.com/screens/fd92ca38-c355-43ad-b15b-878a8d75e169): tall thumbnails with the category under the name.
- [Squarespace template preview](https://mobbin.com/screens/5bfe4629-b9ef-4c2d-9862-2e5811d35681): device toggle top-left, and on the right a grid of colour palettes and font packs that restyle the preview before "Start with this design".
- [Biosites style page](https://mobbin.com/screens/13c9299f-3235-4e63-9b83-3d10c138b5e7): Light and Dark tabs over colour swatches, font tiles showing "Aa", live phone preview beside them.
- [Readymag](https://mobbin.com/screens/3b8a8df4-2ee0-4274-9f43-a4933210ec52): Popular and Newest sort.

**Changes**
1. **Category chips.** One scrolling row: All, then one chip per vertical group (Services, Shop, Clinic, Food, Beauty, Education, Professional, Property, Fitness, Creative, Travel, NGO, Tech). With 15 templates a chip now truly filters. Because most chips match one or two cards, show the matches first and then "Other designs" below, never Relume's empty state **(inferred)**. Preselect the chip from the business profile when it is known.
2. **Style filter.** A second, smaller row: Minimal, Bold, Warm, Corporate, Playful. Needs a `style` field in each template's JSON and in the `site.templates` result, next to `vertical`.
3. **Palette swatches in Preview.** In the Preview dialog, five swatch chips (the palettes of five templates from other families) that restyle the open preview, as Squarespace does. "Use this" then creates the site with the chosen theme. This lets an owner pair Care's layout with Glow's colours without more templates **(inferred)**.
4. **Sample photos in thumbnails.** Text-only cards cannot show a cover or split hero. Bundle one licensed sample photo per template for the picker and Preview only, and never copy it into the created site, where the slot stays an "Add a photo" placeholder **(inferred; needs a product decision on stock photos)**.
5. **Card details.** Keep the 16:10 top-aligned thumbnail. Under the name, show the vertical and style as two small tags and use the pitch from section 2 as the one-liner in place of today's "A website for your … business."

## Binding revisions after design review (supersede earlier sections)

1. Templates keep their own `primary`. The company accent is applied only when the owner ticks "Use my brand colour" in the picker, and only if it passes the contrast check against that template's background; `site.templates` and `site.create` both honour the choice.
2. Every template palette is checked by a Go test for all rendered pairs (text/background, accent-as-text/background, muted text, text on tinted sections, button text on primary, focus ring) at 4.5:1 (3:1 for large text and UI indicators). Tadka's accent and any other failing value are corrected.
3. Templates store no placeholder copy. Empty optional fields render nothing to visitors; the builder (`editing`) shows "Add your …" hints. About is not included in templates by default (its body is required); owners add it by chat or the editor.
4. A Go test fills every template with missing, normal and over-long company data and runs `Validate`; every block has a unique `Type-` + 8-hex id and valid links/anchors.
5. Sections with no substantive content render nothing on the published site (Services, Pricing, FAQ, Testimonials, Gallery, MapHours with neither address nor hours, Text with only a title); header/footer links to a hidden section are omitted. The builder shows the section with an editing hint.
6. `Testimonials` variant `single` renders every item, one per row in a larger quote style; nothing is dropped.
7. `font_heading` becomes a `--site-font-heading` CSS variable (falling back to the body font) used by every heading in preview and published rendering.
8. Catalogue templates (Bazaar, Works) say the owner chooses which items appear; the builder shows the empty Catalogue with a "Choose items" hint and visitors see nothing until items are chosen.
9. The picker: `Template` gains `vertical` and `style`; category chips and a style filter; palette swatches in Preview; `site.create` accepts an optional validated `theme` (the chosen palette) and the brand-colour flag, persisted with the site atomically.
10. The header wraps on narrow screens; an e2e test checks 360 px width, keyboard navigation and no horizontal scroll.
11. Cut from this change: new props or blocks (Hero eyebrow, second CTA, service images, Stats strip, footer band). Templates use only existing blocks and variants. `templateKeys` becomes data-driven (read from the embedded templates directory) instead of a hard-coded list.
