# Website builder: UX spec, Phase 0 (24 Nov 2026)

Source: Mobbin screen study (Wix Aria/Harmony, v0, Squarespace, Lovable, HubSpot, Base44, Gemini canvas, Figma, Replit, Google AI Studio, Wabi, Spellbook, Lightfield, Obvious, Navan, Buffer, Manus, GoDaddy, Universe). Items marked (inferred) were not seen on Mobbin.

## Patterns adopted

**Start screen.** Template grid first; owners recognise more easily than they describe ([Wix](https://mobbin.com/screens/452f0855-815d-4d46-921f-b81733e661c4), [v0](https://mobbin.com/screens/ab76aaa7-0282-49ba-a687-38f59c11de41)). Categories are business verticals as chips ([Squarespace](https://mobbin.com/screens/d89d4798-43a0-4759-99ba-81e7b4e9204f)). Card = thumbnail, name, one-line use case ([Lovable](https://mobbin.com/screens/12582777-7fd5-4840-a8e1-da363e6c0e3e)). A "Use my business info" strip above the grid shows what C Note will fill in (inferred). Explicit Preview button instead of hover (inferred, works on touch). No prompt box in Phase 0.

**Builder layout.** Chat left, fixed 380px; preview fills the rest inside a browser frame holding the address and the desktop/mobile toggle ([Wix editor](https://mobbin.com/screens/db7f2f25-329a-422e-a392-b5280c6bb83c), [Base44](https://mobbin.com/screens/9ec95776-94de-4af3-89ab-9f2e3cf2f191)). Progress shows in both chat and preview ([Gemini](https://mobbin.com/screens/4b99de40-7c78-4042-95e1-fc72086dee52), [Figma](https://mobbin.com/screens/6037f383-fcd1-4a43-9da0-5ea73c7ce673)). History lives in the chat thread as change cards, no separate history screen ([Replit](https://mobbin.com/screens/a4285d31-5f9d-4609-b1e5-43fcaca36361)).

**Chat.** Empty state with 4–5 full-sentence chips; suggestion chips above the composer refresh after each turn ([Base44](https://mobbin.com/screens/9ec95776-94de-4af3-89ab-9f2e3cf2f191)). Selected block appears as a removable "Editing: Hero" chip in the composer ([Wix](https://mobbin.com/screens/deed17d9-1520-46b8-9bc1-119431e97c34)). Each applied change is a card with a plain summary and Undo ([Wabi](https://mobbin.com/screens/fd8cb6af-b1ce-4a3e-aab2-1bb2a43cf988)). Confirm only for "Restore to here" ([Spellbook](https://mobbin.com/screens/21f92e15-f4db-468e-97ee-cef84f8571ca)). Two error types: technical failure = inline danger row with Retry, site untouched ([Lightfield](https://mobbin.com/screens/99d01dcd-a6c6-4a51-8e61-b202b1fe08af)); "can't do that" = normal assistant message with 2–3 alternative chips ([Navan](https://mobbin.com/screens/3e1f3247-a00a-4b1c-bbf1-385792777391)).

**Click to select.** Block level only. Hover: thin outline + name tag. Click: solid outline, toolbar (move up, move down, duplicate, delete), and the Editing chip ([Wix](https://mobbin.com/screens/deed17d9-1520-46b8-9bc1-119431e97c34), [Squarespace](https://mobbin.com/screens/95909c92-a8a7-43f9-b0b2-13e812622f43)). No edit mode, no CSS classes, no layers tree (avoid [Base44 edit mode](https://mobbin.com/screens/e12f9022-8747-4d4b-8060-8545cde7ad9e), [v0 design mode](https://mobbin.com/screens/1ff6cdce-b714-4e59-84f5-684006dd6b5b)).

**Publish (Phase 1).** One modal: subdomain field with the suffix fixed inside it ([Buffer](https://mobbin.com/screens/3f609f34-47ea-421c-8732-dba4f295610a)), then success with Copy, Share on WhatsApp, Visit ([Base44](https://mobbin.com/screens/2b3a1168-240c-4bdf-bd30-15814b830029), [Manus](https://mobbin.com/screens/2ff363b2-8333-47f6-afbd-b5aeed0b3371)).

**Phone.** Preview full screen, composer docked at the bottom, thread in a bottom sheet, tap a block to select, latest change as a toast with Undo, no device toggle ([v0 iOS](https://mobbin.com/screens/8dbadddc-769c-4b68-952e-959cb94a8a40), [Universe iOS](https://mobbin.com/screens/aaa5c261-713f-4eaa-91b4-9d577d7df630)).

## Screen A: Start (no site yet)

```
H1  Create your website
Sub Pick a design. We'll fill in your business details.
┌ Business info strip (settings-row style, icon left) ───────────┐
│ [icon] Sharma Dental · +91 98… · Pune · 6 services    [Edit]   │
│ We'll use this on your site. You can change anything later.    │
└────────────────────────────────────────────────────────────────┘
Chips: [All] [Services] [Shop] [Clinic] [Restaurant] …
Cards: 16:10 thumbnail · name · one-liner · [Preview] [Use this]
       3 columns ≥lg, 2 md, 1 phone
```
Preview opens a full-height modal rendering the template with the owner's data, device toggle, "Use this design". Phase 0 ships one template, so the chips row can be omitted until there are more.

## Screen B: Builder

```
┌ ← Back │ Site name ✎ │ ● Draft │ ↶ ↷ │                [Publish (Phase 1)] ┐  56px
├ CHAT 380px ─────────────┬ PREVIEW (flex-1, muted bg) ──────────────────────┤
│ thread: user bubbles,   │ ┌ ◦◦◦  name.cnote.site            [🖥] [📱] ┐   │
│  change cards [Undo],   │ │ Header                                   │   │
│  assistant one-liners   │ │┌[Hero]───────────────[↑][↓][⧉][🗑]──────┐│   │
│ ─────────────────────── │ ││ selected block                         ││   │
│ chips (scroll-x)        │ │└────────────────────────────────────────┘│   │
│ ┌ composer ───────────┐ │ │ Services / Contact form / Footer         │   │
│ │ [Editing: Hero ×]   │ │ └──────────────────────────────────────────┘   │
│ │ Ask for a change…[➤]│ │  mobile toggle → frame 390px, centred          │
└─┴─────────────────────┴─┴────────────────────────────────────────────────┘
```

## States

| State | Chat | Preview |
|---|---|---|
| Empty | EmptyState "Your site is ready. Tell me what to change." + chips ("Add our phone number", "Make the top section blue", "Change the heading", "Remove a section") | Template rendered, nothing selected |
| Applying | User bubble, then "Updating Hero…" with spinner; send becomes Stop; composer typeable, submit disabled | Old page stays visible; shimmer on target block; thin progress bar under the frame |
| Applied | Change card: check, plain summary, Undo; chips refresh | Scroll to block, outline pulses ~1.5s |
| Can't do | Assistant message with reason + 2–3 alternative chips, no red | Unchanged |
| Error | Danger row "Couldn't apply that. Your site wasn't changed." + Retry; user text kept | Unchanged |
| Undone | Card muted "Undone" + Redo; ↶/↷ act on latest change | Block reverts with the same pulse |
