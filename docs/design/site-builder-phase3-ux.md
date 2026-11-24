# Website builder: UX spec, Phase 3 (24 Nov 2026)

Builds on `site-builder-ux.md`, `site-builder-phase1-ux.md` and `site-builder-phase2-ux.md` (left column `Chat | Edit` at 380px, preview in a browser frame, page switcher in the top bar, 480px settings sheet, domain sheet, stats card). Source: Mobbin screens viewed on 24 Nov 2026; every link is a screen that was actually seen. Items marked **(inferred)** were not seen on Mobbin and are our own judgement.

Not found in the searches run: Dukaan, Instamojo, Meesho, WhatsApp catalogue, Zoho Sites/Commerce, Durable, Hostinger, Ghost, Weglot, any "Enquire on WhatsApp" button, any Indic-script website, and any platform-level Trust & Safety queue with a risk score. Those parts are inferred from the nearest patterns and say so.

## 1. Choosing which invoicing items appear on the site

**References**
- [Shopify Select products](https://mobbin.com/screens/823f4ce6-c480-49cd-9cae-e39662abbffc): modal with search on top, rows of checkbox + thumbnail + name + price, a running count in the footer ("0/500 selected") beside Cancel / Add. The [Manage products](https://mobbin.com/screens/9fadbfbf-d294-402a-b09f-a20bebc2684a) version adds a header select-all and a footer that says what will change: "Adding 1, Removing 0".
- [Fresha](https://mobbin.com/screens/c0a2824a-89da-4011-8b8e-8b79096c65be): "All services 4", then a checkbox on the category heading ("Hair & styling 4") above its indented items, each with a price on the right.
- [Square](https://mobbin.com/screens/a415a2b1-ebb9-4791-a129-538a1198bbaa): "Select categories to add to your menu. We'll use these categories to create customer-facing menu groups", with item counts per category.
- Tax wording: [Wix](https://mobbin.com/screens/6ae4e2b3-3e43-4f18-a61e-02d027fb8f6e) (two radios, "Tax should be added at checkout" / "Tax is already included in my prices") and [PayPal](https://mobbin.com/screens/e4733d69-7731-4720-bf3e-d39b549b9348) (each radio has a one-line explanation).
- Preview beside the controls: [Squarespace product block](https://mobbin.com/screens/0fac83fc-7ee6-45aa-b0f0-402b365df6cd) (Image / Title / Price / Description toggles over the live page) and [Biosites](https://mobbin.com/screens/e6f6e1cc-211b-4ae0-a64b-c7f5351b09c2) ("Show Price" toggle, phone preview on the right).

**Pattern to copy.** A full-height modal opened from the Catalogue block's first field in Edit (`[Choose items]`) and from the chat ("Show my products on the site"). Left: search, filter chips, categories as collapsible groups whose heading checkbox selects the whole category (Fresha), three-state when partly chosen. Right: a phone-width preview of the real block that updates on every tick (Biosites). Footer says what will change (Shopify). The price choice is two radios with a worked example in rupees, not the words "inclusive/exclusive". Items come from invoicing and are not edited here; the row links out to the item. Items with no photo are allowed but counted, with "Add photo" on the row (inferred; no reference showed a missing-image check). Out-of-stock items stay ticked but are not shown, and the row says so (inferred).

```
┌ Choose what to show on your website ──────────────────────────────────────── × ┐
│ 🔍 [ Search your items                 ]            │  PREVIEW  [📱] [🖥]       │
│ (All 86) (On website 24) (No photo 6) (Out of stock)│ ┌──────────────────────┐ │
│                                                     │ │ Our products         │ │
│ [■] Sweets                         8 of 12   ⌃      │ │ (All)(Sweets)(Snacks)│ │
│   [✓] [img] Kaju katli        ₹1,180 / kg           │ │ ┌──────┐ ┌──────┐    │ │
│   [✓] [img] Motichoor laddu     ₹560 / kg           │ │ │ img  │ │ img  │    │ │
│   [✓] [ ▨ ] Mysore pak          ₹640 / kg           │ │ └──────┘ └──────┘    │ │
│             No photo · [Add photo]                  │ │ Kaju katli  Laddu    │ │
│   [✓] [img] Rasgulla (tin)      ₹240 / tin          │ │ ₹1,180/kg   ₹560/kg  │ │
│             Out of stock · hidden until back        │ │ incl. GST            │ │
│   [ ] [img] Soan papdi          ₹320 / box          │ └──────────────────────┘ │
│ [ ] Snacks                         0 of 9    ⌄      │                          │
│ [✓] Gift boxes                     5 of 5    ⌄      │ PRICES ON YOUR WEBSITE   │
│ [ ] No category                    0 of 3    ⌄      │ (•) Price with GST       │
│                                                     │     ₹1,180               │
│ ⚠ 6 chosen items have no photo. They'll show a      │ ( ) Price + GST          │
│   plain tile. [Show them]                           │     ₹1,000 + GST         │
│                                                     │ ( ) Don't show prices    │
│                                                     │     "Ask for price"      │
├─────────────────────────────────────────────────────┴──────────────────────────┤
│ 24 of 86 items on your website · adding 3, removing 1        [Cancel] [Save]   │
└────────────────────────────────────────────────────────────────────────────────┘
```
New items added to a fully ticked category appear on the site automatically; a partly ticked category does not grow by itself (inferred). On a phone the preview becomes a `[Preview]` button in the footer. The item's own tax setting in invoicing decides the arithmetic; the radio only decides what is displayed.

## 2. Published catalogue on mobile

**References**
- [Snoonu](https://mobbin.com/screens/e0eea996-ddeb-482b-b64e-1118c1f99a23): horizontal category chips with the current one filled, grid of photo tiles, price first in bold, name on two lines below.
- [Faire](https://mobbin.com/screens/eef344ac-8bcd-4675-a0eb-044d9649b12f): two-column grid; bold price, two-line name, and a third grey line for the unit ("Case of 1").
- [Zalando](https://mobbin.com/screens/4225f73f-a85f-4cd9-bfa6-b64a9d6b8fc6): "£254.99 VAT included" as small grey text on the same line as the price.
- [Artsy](https://mobbin.com/screens/8637fa54-2d14-4929-a6ca-69294e21820a): "Questions about this piece? [Contact Gallery]" as a secondary row, with the price and the main button pinned to the bottom of the screen.
- [Airtasker](https://mobbin.com/screens/d5b9b83b-da39-4d58-a1f9-a123d623d068): "Have a question? Need something specific? Start a conversation" above a soft secondary button.

**Pattern to copy.** No cart, no checkout. Two columns on a phone, four on desktop; square photo, name (two lines), price with unit on one line ("₹1,180 / kg"), tax note once per block rather than on every tile (inferred; Zalando repeats it per product). Category chips scroll sideways and stick under the header. A list variant (no photos, name left, price right) is the second layout for service and rate-card businesses (inferred). Tapping an item opens a bottom sheet on phones and a centred modal on desktop, not a new page, so Back returns to the same scroll position (inferred); each item still has its own address for sharing. The sheet pins two buttons: **Enquire on WhatsApp** (primary, opens `wa.me` with "Hi, I'm interested in Kaju katli (₹1,180 / kg) – sharmasweets.in/p/kaju-katli") and **Ask a question** (secondary, the Phase 1 contact form with the item attached, which lands in Leads). Both buttons are inferred from Artsy/Airtasker; if the site has no WhatsApp number only "Ask a question" shows. Out-of-stock items are simply absent: no greyed tile, no "Sold out" badge.

```
MOBILE 390                       ITEM SHEET (mobile)          DESKTOP ≥1024
┌─────────────────────────┐      ┌─────────────────────────┐  ┌──────────────────────────────────────────┐
│ ☰  Sharma Sweets   हिन्दी │      │          ───            │  │ Our products                             │
├─────────────────────────┤      │ ┌─────────────────────┐ │  │ (All) (Sweets) (Snacks) (Gift boxes)     │
│ Our products            │      │ │   photo  ● ○ ○      │ │  │ ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐          │
│ (All)(Sweets)(Snacks) → │      │ └─────────────────────┘ │  │ │ img │ │ img │ │ ▨   │ │ img │          │
│ ┌──────────┐┌──────────┐│      │ Kaju katli              │  │ └─────┘ └─────┘ └─────┘ └─────┘          │
│ │  photo   ││  photo   ││      │ ₹1,180 / kg  incl. GST  │  │ Kaju    Laddu   Mysore  Soan             │
│ └──────────┘└──────────┘│      │ Made fresh every        │  │ katli           pak     papdi            │
│ Kaju katli  Motichoor   │      │ morning with pure ghee… │  │ ₹1,180  ₹560    ₹640    ₹320             │
│             laddu       │      │ Sweets                  │  │ / kg    / kg    / kg    / box            │
│ ₹1,180 / kg ₹560 / kg   │      ├─────────────────────────┤  │ Prices include GST.       [Show 12 more] │
│ ┌──────────┐┌──────────┐│      │ [🟢 Enquire on WhatsApp]│  └──────────────────────────────────────────┘
│ │ ▨ name   ││  photo   ││      │ [   Ask a question    ] │   item opens as a 2-column modal:
│ └──────────┘└──────────┘│      └─────────────────────────┘   photo left · name, price, text, buttons right
│ Prices include GST.     │
│ [ Show 12 more ]        │       LIST LAYOUT (services)
└─────────────────────────┘       Root canal ........ from ₹3,500
                                  Braces consultation ...... ₹500
```
No-photo tile: the item name on a tint of `--site-primary`, never a broken-image icon (inferred). "Show more" is a button, not infinite scroll, so the footer stays reachable (inferred).

## 3. Blog

**References**
- Post list: [Wix](https://mobbin.com/screens/2c7ac668-5f4e-47c7-ad2a-6ea850d4cc75) (tabs with counts: Published (1), Drafts (0), Scheduled (1); thumbnail, title, age) and [Substack](https://mobbin.com/screens/83bb6383-b847-43c8-b925-d59334e9f997) (a three-way segmented control, rows grouped by month, "Updated 1:18 PM").
- [GoDaddy](https://mobbin.com/screens/45752a48-a025-44d2-8b36-dba739ed95cf): a banner above the list, "Add a Blog Section to Your Site: in order for people to read your posts you'll need to add a blog section".
- Editor: [Kajabi](https://mobbin.com/screens/e5540745-d1f5-4e2d-a407-411595342ae1) (Title, Content, Visibility radios with "Set a specific publishing date", Blog Image with "Recommended dimensions of 1280×720" and an alt field) and [beehiiv](https://mobbin.com/screens/4c568027-8f5b-45e2-94ff-adafa9ed70d4) ("Draft · Synced" in the top bar, settings in a side panel).
- AI draft with review: [HubSpot](https://mobbin.com/screens/5520f4b4-5a75-4a6f-9f37-948d101f4e59) (step 4 of 4 "Review blog post outline": "Your blog post draft will be made from what's on this screen", editable headings, then "Generate blog post") and [Semrush](https://mobbin.com/screens/91364821-6e3c-40e2-b7a6-c200268fabd0) (chat on the left, the article on the right with Title 46/60 and Description counters).
- Published list: [Equals](https://mobbin.com/sites/sections/cc4d6105-96e5-44d6-8c0e-5db296defe35) (thumbnail left, title, "date | 3 min read", two-line excerpt) and [ClassPass](https://mobbin.com/sites/sections/79c2c07e-05a6-4261-8432-4344ab6f5402) (an "Older posts" button).
- Reading on mobile: [Medium](https://mobbin.com/screens/a7822048-31b1-43de-b57f-cf6b10e01924) (title, one-line subtitle, "4 min read · date", author, then the cover) and [Podia](https://mobbin.com/screens/38837591-84bb-4c6b-8254-b10120bb6ab7) (cover, date, title, author).

**Pattern to copy.** Blog is one more page in the page switcher. With it open, Edit shows the post list instead of the section list; opening a post puts it in the preview frame, where title and body are typed in place, and the left column holds its settings (inferred; this keeps the two-column builder instead of adding a Wix-style dashboard). Three statuses only. The toolbar is short: heading, bold, list, link, quote, photo. No HTML, no embed code. Chat's "Write a post about…" never writes straight away: it replies with a card holding a title and 3–5 headings to edit (HubSpot), and "Write draft" creates a **draft** opened under the Phase 2 banner wording "Draft written by C Note AI. Please read before publishing". The AI uses only facts from the business profile and the owner's message and never invents prices, awards or customer quotes (inferred, same rule as testimonials). Publishing a post is its own button; it does not publish other pending site changes (inferred).

```
┌ ← │ Sharma Sweets ✎ │ [Blog ▾] │ ● Live │ ↶ ↷ │ ✓ Saved          [⏱][⚙] [Publish changes] ┐
├ [ Chat | Edit ] 380px ───────┬ PREVIEW ─────────────────────────────────────────────────┤
│ Posts            [+ New post]│ ┌ ◦◦◦ sharmasweets.in/blog                [🖥] [📱] ┐   │
│ [Published 4|Scheduled 1|    │ │ Blog                                             │   │
│  Drafts 2]                   │ │ ┌────┐ How we make kaju katli                    │   │
│ ┌──┐ Diwali gift box guide   │ │ │img │ 12 Nov 2026 · 3 min read                  │   │
│ └──┘ Draft · edited 2:10 pm ⋯│ │ └────┘ Every morning at 5 we start with…         │   │
│ ┌──┐ 5 sweets for weddings   │ │ ┌────┐ Why we use pure ghee                      │   │
│ └──┘ Draft · by C Note AI   ⋯│ │ └────┘ 2 Nov 2026 · 2 min read                   │   │
│ ✨ Write a post with AI       │ │                 [ Older posts ]                  │   │
└──────────────────────────────┴─┴──────────────────────────────────────────────────┴───┘

POST OPEN (left column)            PREVIEW = the post, typed in place
┌ ← Posts          ● Draft      ┐  ┌ ⚠ Draft written by C Note AI. Please read before ┐
│ Cover photo                   │  │   publishing.                                    │
│ [thumb] Change · Remove       │  ├ [H] [B] [•] [🔗] [❝] [🖼] ──────────────────────┤
│ Short summary          96/160 │  │ [cover photo 16:9]                               │
│ [Shown in the list and on   ] │  │ Diwali gift box guide            ← title         │
│ Address /blog/[diwali-gift… ] │  │ 24 Nov 2026 · 3 min read                         │
│ ▸ How it appears on Google    │  │ Choosing a box for family is…    ← body          │
│ ▸ How it looks when shared    │  │ ## For the office                                │
│ When to publish               │  │ ✨ select text → Shorter · Simpler · Rewrite      │
│ (•) Now  ( ) On [date][time]  │  └──────────────────────────────────────────────────┘
│ [Preview]      [Publish post] │
└───────────────────────────────┘

CHAT REVIEW CARD
│ you: Write a post about our Diwali gift boxes                  │
│ ┌ Here's a plan. Change anything, then I'll write it. ───────┐ │
│ │ Title [Diwali gift box guide                             ] │ │
│ │ ⠿ [Which box for family                                ] ⊖ │ │
│ │ ⠿ [For the office                                      ] ⊖ │ │
│ │ ⠿ [How to order on WhatsApp                            ] ⊖ │ │
│ │ + Add a point           Length (Short)(Medium)             │ │
│ │                              [Not now] [Write draft]       │ │
│ └────────────────────────────────────────────────────────────┘ │
```
Published post page: one column, max 680px, 17–18px body, line height 1.6, cover first, then title, date and reading time, body, and at the end the site's WhatsApp / contact block and "More posts" (sizes inferred). Blog page not in the menu: GoDaddy-style banner over the post list, "Your blog isn't in your menu yet. [Add to menu]".

## 4. Languages

**References**
- [Wix Add a new language](https://mobbin.com/screens/594cd42c-70e3-407e-8461-31104c240210): language select, "How do you want to start translating? Manual / Machine translation", "Make site in Chinese visible right away? No, keep it hidden for now", and a footnote that not all text may be translated accurately.
- [Framer Localization](https://mobbin.com/screens/a2248603-8b0d-45e1-8284-c9658a380771): two columns (English | Spanish), rows grouped under page headings, a "NEW" tag per row, per-locale percentage in the sidebar.
- [Etsy](https://mobbin.com/screens/45b8c2f0-6841-44f5-ac11-caa22aceabed): original in grey on the left, editable translation on the right, "Edit original (English)" link per group.
- [Ditto](https://mobbin.com/screens/5c169c81-72e9-4744-a78a-4add65b51324): the selected text is outlined in the page preview while its translation and an "In Review" status sit in the side panel.
- [Customer.io](https://mobbin.com/screens/4a9501e1-3c81-4119-b520-b344a0e7c267): "Missing content" tag and a Live toggle per language.
- Switcher: [Coda header](https://mobbin.com/sites/sections/5267d6c6-eeca-4050-bbae-119af1bb9106) (control at the right of the header, before the buttons), [Aboard](https://mobbin.com/screens/4be155ba-7ba0-4ceb-8d26-ce773a2f0094) (a small top-right pill). Naming: [Discord](https://mobbin.com/screens/cbaadee3-354e-4573-acfb-7bcb28eb4d1d) and [Notion](https://mobbin.com/screens/efd159f7-2317-46c1-bca2-bfc495769903) list each language in its own script ("हिंदी") with the English name small underneath.

**Pattern to copy.** Settings gets a "Languages" group; Add is one small modal (Wix) with AI translation as the default and "keep hidden until I've checked" pre-selected. Translation runs in the background per page. The owner reviews in a wide sheet: a page list with progress on the left, and for the chosen page two columns of text, original read-only, translation editable (Framer, Etsy). Every AI-translated row starts as "Please check"; the owner clears rows one at a time or with "Mark page as checked". Clicking a row scrolls the preview to that block (Ditto). Names, phone numbers, addresses, testimonials and prices are copied, not translated (inferred, follows the Phase 1 verbatim rule). When the original text later changes, that row returns to "Please check" with the reason "English text changed" (inferred). A language chip in the top bar beside the page switcher (`[EN ▾]`) chooses which language Chat, Edit and the preview work on (inferred).

```
ADD (modal)                                     SETTINGS › LANGUAGES
┌ Add a language ──────────────────────── × ┐   English          Main language
│ Language  [ हिन्दी · Hindi             ▾ ] │   हिन्दी · Hindi    ◌ Translating 3 of 5 pages
│ (•) Translate with C Note AI              │   தமிழ் · Tamil     ⚠ 14 to check  ◯ Hidden [Review]
│     You check it before visitors see it.  │   + Add a language
│ ( ) I'll translate it myself              │
│ [✓] Keep hidden until I've checked        │
│ AI translation can make mistakes.         │
│                    [Cancel] [Add language]│
└───────────────────────────────────────────┘

REVIEW (sheet, 960px; stacked on phone)
┌ Check your Hindi website ────────────────────────────────────────────────── × ┐
│ PAGES              │ Home · 9 of 14 checked             [Mark page as checked]│
│ Home      ⚠ 5 left │ ENGLISH (original)            │ हिन्दी                     │
│ Services  ✓        │ HERO                          │                          │
│ About     ◌ …      │ Fresh sweets, made every      │ [हर सुबह ताज़ी बनी       ] │
│ Contact   ⚠ 3 left │ morning                       │ [मिठाइयाँ              ] ⚠│
│ Blog (2)  ○ not    │                               │        [Looks right ✓]   │
│           started  │ Order on WhatsApp             │ [WhatsApp पर ऑर्डर करें ] ✓│
│                    │ SERVICES                      │                          │
│ ▓▓▓▓▓▓░░░ 62%      │ Kaju katli                    │ काजू कतली  (item name) 🔒 │
│                    │ [Edit English]                │ ✨ Translate again        │
├────────────────────┴───────────────────────────────┴──────────────────────────┤
│ 8 left to check. Visitors can't see Hindi yet.      [Close] [Show to visitors]│
└────────────────────────────────────────────────────────────────────────────────┘
```
"Show to visitors" is enabled with rows still unchecked but confirms: "8 lines haven't been checked. Show Hindi anyway?" (inferred). Pages not yet translated fall back to the main language with the switcher still visible.

**Published switcher (header).**
```
TWO LANGUAGES: one link naming the other one     THREE OR MORE: globe menu
┌──────────────────────────────────────┐         ┌───────────────────────────────┐
│ Sharma Sweets   Home Products  हिन्दी  │         │ Sharma Sweets  Home …  🌐 EN ▾ │
└──────────────────────────────────────┘         └───────────────────┬───────────┘
MOBILE: stays in the header bar, outside ☰                           │ English  ✓│
┌──────────────────────────────┐                                     │ हिन्दी     │
│ ☰  Sharma Sweets       हिन्दी  │                                     │ தமிழ்     │
└──────────────────────────────┘                                     └───────────┘
```
Names are always in their own script; no flags, because the languages share one country (inferred; Coda uses a flag). The link keeps the visitor on the same page (`/hi/services`). No automatic redirect by browser language; a first-visit choice is remembered (inferred). 44px touch target.

**Indic-script typography (all inferred; no Indic site was found).** Load the matching Noto Sans face (Devanagari, Tamil, Telugu, Kannada, Bengali, Gujarati, Malayalam, Gurmukhi) only for languages in use, and set `lang` on the page. Body line height 1.7 (Latin 1.5) and headings 1.35, because matras and stacked conjuncts sit above and below the line. No uppercase transform, no letter-spacing, no italics, no weight under 400. Size up about 10% against the Latin scale. Never cut text mid-word with an ellipsis by character count; clamp by lines. Buttons and menu items must wrap or grow: Hindi and Tamil run 20–40% longer than English. Character counters in the builder count visible characters (grapheme clusters), not code points. Digits stay 0–9 and prices stay "₹1,180".

## 5. Platform-admin abuse review

**References**
- [Reddit Queue](https://mobbin.com/screens/c20fd251-fbfe-471b-b3a4-c0175cf1ada4): tabs Needs Review / Reported / Removed, the reason as a coloured tag on the item ("This is spam"), Approve and Remove inline. Its [removal dialog](https://mobbin.com/screens/8be601d7-35dd-45a7-9df8-b1fc1874cde9) picks a reason, pre-fills the message to the user, and has a separate internal "Mod note".
- [Circle Moderation](https://mobbin.com/screens/6c0687e1-5bf7-47b8-8e01-1d7a57f99b89): Inbox / Approved / Rejected with counts; columns Type, Author, Reported by, Published, Reports.
- [Whop](https://mobbin.com/screens/03413816-d3ca-4486-b76a-395028847517): a score on a green-to-red bar, "What impacts your score?" listing each contributing signal, and the threshold stated in words.
- [Remote](https://mobbin.com/screens/5a02d53a-4cc4-411a-ba45-3d0b7c1f0f54): the thing under review previewed on the left, status, timeline and Decline on the right.
- Visitor report: [TikTok](https://mobbin.com/screens/9b9752aa-e448-4236-9e51-9cdcb4d1b5ee) (one list of reasons), [Glassdoor](https://mobbin.com/screens/2033c3c9-fad8-476d-ab26-946f28521398) (optional detail, 200 characters), [GetYourGuide](https://mobbin.com/screens/97775870-cbac-4f18-8c85-1fa3200168a1) ("Thanks for letting us know. We'll review…").
- Owner notice: [TikTok Studio](https://mobbin.com/screens/50fc627d-79ca-4b2b-810a-9b5cc9423a91): what happened, the item, "Reason" with a name and one plain sentence, a policy link and one Appeal button.

**Pattern to copy.** A queue sorted by score, then a split detail view. The score is never shown alone: each signal is a line with the exact text or link that triggered it (Whop). The preview is a screenshot taken at scan time plus a sandboxed live render with links and forms disabled, so the reviewer never visits a phishing page (inferred). Three actions. **Take down** needs a reason and sends the owner a message; **Dismiss** needs a one-line note and stops the same signals re-flagging the site until its content changes (inferred); **Ask owner** keeps the site live, sets a reply deadline and moves the row to "Waiting for owner". Every action is logged with the reviewer's name. Visitor reports come from a "Report this website" link in the footer badge area of every published site (placement inferred).

```
┌ Site reviews        [Needs review 7 | Waiting for owner 2 | Taken down 31 | Dismissed 112] ┐
│ 🔍 [ address or workspace ]         Source (All ▾)  Reason (All ▾)      Sort: Score ▾      │
│ SCORE  SITE                       TOP REASON             SOURCE              FLAGGED       │
│ ■ 92   sbi-kyc-update.cnote.site  Bank name + login form  Scan · 3 reports   12 min ago    │
│ ■ 71   quickloan24.in             Asks for card details   Scan               1 h ago       │
│ ■ 40   sharmadental.in            "Spam" (visitor)        1 report           3 h ago       │
└────────────────────────────────────────────────────────────────────────────────────────────┘

DETAIL
┌ ← sbi-kyc-update.cnote.site   ■ 92 High   Live since 23 Nov      [‹ 1 of 7 ›]              ┐
├ PREVIEW (safe: links and forms off) ────┬ WHY IT WAS FLAGGED ─────────────────────────────┤
│ [At scan 24 Nov 2:02 pm | Now]  [📱][🖥] │ +40 Uses a bank's name: "SBI KYC Update"  → Hero │
│ ┌─────────────────────────────────────┐ │ +30 Asks for a password or OTP           → Form │
│ │ screenshot; the matched text is     │ │ +15 Link goes to bit.ly/…               → Button│
│ │ outlined, click a reason to jump    │ │ + 7 Workspace is 2 hours old, 0 invoices        │
│ └─────────────────────────────────────┘ │ VISITOR REPORTS (3)                             │
│ Pages: Home · Verify (2)                │ "Asking for my ATM PIN" · Scam · 24 Nov 1:40 pm │
│                                         │ OWNER  Ramesh K · ws_91f… · 1 other site        │
│                                         │ HISTORY  none                                   │
│                                         │ Internal note [                              ]  │
├─────────────────────────────────────────┴─────────────────────────────────────────────────┤
│ [Dismiss]            [Ask owner]                                    [Take down] (danger)  │
└───────────────────────────────────────────────────────────────────────────────────────────┘
TAKE DOWN (dialog): Reason [Pretending to be another business ▾] · Message to owner (pre-filled,
editable) · [✓] Also stop this workspace publishing new sites · [Cancel] [Take down site]

VISITOR REPORT (published site)          OWNER MESSAGE (email + WhatsApp + banner in the builder)
┌ Report this website ───────── × ┐      ┌ Your website has been taken offline ──────────────┐
│ ( ) Scam or fraud               │      │ sbi-kyc-update.cnote.site · 24 Nov 2026, 2:20 pm  │
│ ( ) Pretending to be someone    │      │ Reason: Pretending to be another business         │
│ ( ) Illegal or harmful          │      │ The page uses a bank's name and asks visitors for │
│ ( ) Spam                        │      │ a password. This isn't allowed on C Note.         │
│ ( ) Something else              │      │ Your invoices and other C Note data are not       │
│ Tell us more (optional)   0/200 │      │ affected. Nothing has been deleted.               │
│ [                             ] │      │ Think this is a mistake? A person will look at it │
│            [Cancel] [Send]      │      │ within 2 working days.                            │
└─────────────────────────────────┘      │ [Read the rules]              [Ask for a review]  │
 then: "Thanks. We'll look at this."     └───────────────────────────────────────────────────┘
```
"Ask for a review" opens one text box ("Tell us what we got wrong", 500) and returns the site to the queue tagged Appeal, shown to a different reviewer (inferred). One appeal per takedown; the 2-working-day promise is a placeholder. No login is needed to report; reports are rate-limited per visitor (inferred). The taken-down address serves a neutral "This website is not available" page.

## 6. Custom domain through Cloudflare

**References**
- [Cloudflare domains](https://mobbin.com/screens/0aae9f74-80bb-48c5-97c8-061b84c9430f): one status word per domain ("Active", "Invalid nameservers").
- [AWS Amplify](https://mobbin.com/screens/1d706922-62c3-433e-a760-244137f06a5e): three steps, SSL creation → SSL configuration → Domain activation, with "This may take a few minutes".
- [Squarespace](https://mobbin.com/screens/6a83a561-7ee4-4562-a698-dff65771ae44): "SSL Certificate: Issued" and one sentence that it renews by itself.
- [Dub](https://mobbin.com/screens/49e9ef47-11c4-4468-900b-c7c2deaa2803): Active / Pending / Invalid pills.

**Finding: the owner-facing sheet does not change.** Same three steps, same two record cards, same wording. Cloudflare's names (hostname, pending validation, active, zone) never appear. Differences to allow for, all to confirm with the technical design:

| What may differ | What the owner sees |
|---|---|
| Record values | Only the values inside the two cards (placeholders in Phase 2). Labels, Copy buttons and registrar chips are unchanged |
| Certificate timing | Same line "Securing your site (https)… usually a few minutes". If it passes 30 minutes: "Taking longer than usual. Nothing for you to do." (inferred) |
| Domains connected before the move | No action and no notice if the old records keep working. If records must change: a banner in Settings, "Please update 1 record for sharmadental.in by {date}", opening the same sheet at step 2 with only the changed card highlighted (inferred) |
| Owner's domain is itself on Cloudflare with proxying on | New error on the record card: "This record is set to 'Proxied' at Cloudflare. Change it to 'DNS only' and check again." (inferred; verify this failure exists) |
| Domain claimed by another account at the provider | Reuse "This domain is connected to another website" + Talk to a person |
| Site taken down (§5) | Domain row shows `● Offline · taken down` instead of Live; Disconnect still works so the owner keeps their exit right (inferred) |

## 7. Share preview image

**References**
- [WhatsApp](https://mobbin.com/screens/a37cfd91-3929-479e-b4bb-8608d7b95641): the real card inside a chat bubble: wide image, bold title, two lines of description, link icon with the domain.
- [Framer](https://mobbin.com/screens/ffa32c13-fb54-460b-b210-cbc5d94eb662): "Social Preview · 1200 × 630 pixels · Upload" with the image shown beside it, directly under the search preview.
- [Workable](https://mobbin.com/screens/c20ac3ac-2ce4-4b33-bdee-16f6bdb27952): a mock card on the left that already shows a default image, upload and description on the right, and "It may take some time for each social network to reflect changes".
- [Wix](https://mobbin.com/screens/f105bb4f-2ff6-4fb4-ae69-53f862f096c8): "Preview on X" card. Avoid its `og:title` variable chips and card-size radios.
- [X Articles](https://mobbin.com/screens/150f0664-92dc-4c79-a003-8b0b3642082f): "Timeline preview" shown in the publish dialog itself.

**Pattern to copy.** A group in Settings directly under "How you appear on Google", and the same group per page and per post. The card is drawn as a WhatsApp bubble first, because that is where owners share (inferred; no builder showed a WhatsApp-styled preview); Facebook and X are a second tab using the same image and text. There is always an image: by default C Note makes one from the hero photo, site name and theme colour (Workable's default), so the owner only acts to change it. Title and description are the Google fields, not asked for twice. The publish success view shows the same card above "Share on WhatsApp" (X Articles).

```
HOW YOUR LINK LOOKS WHEN SHARED
[ WhatsApp | Facebook & X ]
        ┌───────────────────────────────┐
        │ ┌───────────────────────────┐ │   Picture
        │ │   picture 1200 × 630      │ │   (•) Made for you from your site
        │ └───────────────────────────┘ │   ( ) Your own photo  [Choose photo]
        │ Sharma Sweets | Sweets in Pune│   Wide photos work best (1200 × 630).
        │ Fresh sweets made every       │   Name and description come from
        │ morning in Kothrud…           │   "How you appear on Google" above.
        │ 🔗 sharmasweets.in            │
        │                      2:41 pm ✓│   Apps keep the old picture for a
        └───────────────────────────────┘   while after you change it.
```
"Choose photo" opens the Phase 2 photo picker; a tall photo gets a crop step fixed to the card's shape (inferred). Posts default to their cover, catalogue items to the item photo.

## States

| State | Where | What is shown |
|---|---|---|
| No items in invoicing | Item picker; Catalogue block | Picker: "You haven't added any items yet. Add them in Items and they'll appear here." `[Go to Items]`. Block in the builder: dashed placeholder "Your products will appear here"; on the published site the block is not rendered |
| Items exist, none chosen | Catalogue block | Builder placeholder + `[Choose items]`; published: not rendered |
| Items without images | Picker row, picker summary, published tile | Row: `▨ No photo · [Add photo]`. Summary: "6 chosen items have no photo." Published: name on a tinted tile; in the list layout nothing differs |
| All chosen items out of stock | Catalogue block | Builder: "All 5 chosen items are out of stock, so visitors see nothing here." Published: block not rendered (inferred) |
| Item price is zero or missing | Published tile | "Ask for price" instead of ₹0 (inferred) |
| Translation in progress | Settings › Languages; review sheet | `◌ Translating 3 of 5 pages`; finished pages can be reviewed already; the language stays hidden; safe to close |
| Translation needs review | Languages row; page list; row | `⚠ 14 to check` + `[Review]`; per row an amber dot and `[Looks right ✓]`; after an edit to the original: "English text changed" |
| Translation checked, hidden | Languages row | `✓ Checked · ◯ Hidden` + `[Show to visitors]` |
| Translation live | Languages row; top bar chip; published header | `● Live at /hi`; switcher appears on the published site at the next publish |
| Translation failed | Languages row | "Couldn't translate 2 pages. Your website is unchanged." `[Try again]`; translated pages are kept |
| Post draft | Post list; post top line | Grey `Draft · edited 2:10 pm`; AI drafts add "by C Note AI" and the read-before-publishing banner; not visible to visitors |
| Post scheduled | Post list; post settings | Blue `Scheduled · 28 Nov, 9:00 am` + `[Change]`; editing keeps the schedule; missed schedule: "Couldn't publish at 9:00 am. [Publish now]" (inferred) |
| Post published | Post list; preview | Green `Published · 12 Nov`; later edits show `Published · changes not live` + `[Update post]`; ⋯ has "Move back to drafts" |
| No posts yet | Post list | "No posts yet. Share news, tips or offers." `[+ New post]` `[✨ Write a post with AI]`; published `/blog` is not linked or rendered until one post is published |
| Scan flagged | Admin queue. Owner sees nothing | Row in Needs review with score band (0–39 grey, 40–69 amber, 70+ red; bands are placeholders); the site stays live until a person acts, except above an auto-hold threshold to be set by policy (inferred) |
| Waiting for owner | Admin queue; owner's builder | Admin: row with reply-by date. Owner: amber banner "We have a question about your website. Please reply by 27 Nov." `[Reply]`; site stays live |
| Scan dismissed | Admin Dismissed tab | Row with reviewer, time and note; the owner is never told; identical signals don't re-flag until content changes |
| Taken down | Admin; owner's builder, Settings, domain row; public address | Admin: Taken down tab with reason and `[Restore]`. Owner: red banner with the reason and `[Ask for a review]`; Publish disabled; editing still allowed. Visitors: "This website is not available" |
| Appeal sent / decided | Owner banner; admin queue | Owner: "Review requested 24 Nov. We'll reply within 2 working days." Then either "Your website is back online" or "We've kept the decision" with the reason. Admin: row tagged `Appeal` |
| Share image not set | Settings share group | Never empty: the generated picture is shown with "Made for you from your site" |
