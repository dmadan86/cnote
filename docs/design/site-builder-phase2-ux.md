# Website builder: UX spec, Phase 2 (24 Nov 2026)

Builds on `site-builder-ux.md` and `site-builder-phase1-ux.md` (chat left 380px, preview right in a browser frame, change cards with Undo, block toolbar, publish modal, settings sheet, edits meter). Source: Mobbin screens viewed on 24 Nov 2026; every link is a screen that was actually seen. Items marked **(inferred)** were not seen on Mobbin and are our own judgement. DNS values, limits and registrar menu names are placeholders to confirm before build.

## 1. Connect your own domain

**References**
- [Canva](https://mobbin.com/screens/ba9b6732-2050-4aaa-877f-e863a54399a9): titled "Go to your domain provider's website"; one accordion per record, two fields labelled "Name / Host" and "Value / Points to", each with Copy, and a "Record added" button per step.
- [Peerlist](https://mobbin.com/screens/2fbdf077-5875-40d8-8a11-770e14051960): Step 1 field with "Do not use http:// or https://", Step 2 records, "Domain not configured yet" beside "Check configuration", and help chips per registrar (GoDaddy, Namecheap, Cloudflare).
- [Resend](https://mobbin.com/screens/6f41111b-5a31-4d83-a3fa-ec3f2064a957): three-step timeline (Domain added → DNS verified → Verifying domain) with timestamps, and a banner "Looking for DNS records: this may take a few hours".
- [Klaviyo](https://mobbin.com/screens/0e628465-e3f8-4149-96b5-bef342c93a8e): "Your records are verifying. You can close this page. Most DNS records update within a few minutes, but can take up to 48 hours. We'll send you a notification."
- Errors: [Upwork](https://mobbin.com/screens/2e7a6288-4a64-4e8c-a33e-4868ba78824f) says what it expected ("The TXT record you added isn't an exact match. We expected …"); [AutoSend](https://mobbin.com/screens/2c9c4317-dfc7-4957-a9ca-79169ad9bc97) shows a status per record and a Refresh button; [Cofounder](https://mobbin.com/screens/cef5df13-39b7-4b2f-a6bf-786825687713) warns "Leave MX, SPF, DKIM and DMARC records unchanged".
- Disconnect: [Cloudflare](https://mobbin.com/screens/60d5ee01-3cec-4132-a135-7fc57f8f5f9e) ("This does not change your domain registration"), [Render](https://mobbin.com/screens/ea3ccdd9-58d3-47dc-8292-f245e5ce8477) ("This will not delete … You can always connect later").

**Pattern to copy.** A three-step sheet opened from Settings → Address → "Use your own domain". Step 1 is one field. Step 2 shows exactly two records as cards (not a table), fields named the way registrars name them, one Copy button per value. Step 3 is a checklist that checks itself every 30 seconds (inferred interval) and can be closed; we notify on WhatsApp/email when live (channel inferred). Errors name the record, what we found and what we expected. The `.cnote.site` address keeps working throughout and after disconnect.

```
┌ Use your own domain ──────────────────────────────── × ┐
│ ① Your domain                                          │
│ [ sharmadental.in                        ] [Continue]  │
│ Type it without https:// or www.                       │
│ Don't have one? Buy it from any registrar, then return.│
│ ⓘ Your domain stays yours. You keep it at your         │
│   registrar and can point it elsewhere any time.       │
│                                                        │
│ ② Add 2 records at your registrar                      │
│ Where did you buy it? (GoDaddy)(BigRock)(Hostinger)(Other)│
│ → GoDaddy: My Products › Domains › DNS › Add New Record│
│ ┌ Record 1 ─────────────────────── ○ Not found yet ┐  │
│ │ Type  A        Name/Host  @            [Copy]     │  │
│ │                Value      203.0.113.10 [Copy]     │  │
│ ├ Record 2 ─────────────────────── ○ Not found yet ┤  │
│ │ Type  CNAME    Name/Host  www          [Copy]     │  │
│ │                Value      sites.cnote.site [Copy] │  │
│ └───────────────────────────────────────────────────┘  │
│ Leave your email (MX) records as they are.             │
│ Remove any other A record for @.                       │
│                              [I've added the records]  │
│ ③ Status                                               │
│ ✓ Records found                          2:41 pm       │
│ ◌ Securing your site (https)…  usually a few minutes   │
│ ○ Live at sharmadental.in                              │
│ You can close this. We'll message you when it's live.  │
│ sharmadental.cnote.site keeps working meanwhile.       │
└─────────────────────────────── [Check again] [Close] ──┘
```
Registrar chip swaps the one-line path and the field labels to that registrar's wording (inferred; Peerlist only links out). "Other" shows generic wording plus "Talk to a person". Once live, Settings shows `sharmadental.in ● Live` with `[Disconnect]`.

**Disconnect confirmation:** "Disconnect sharmadental.in?" / "Your website goes back to sharmadental.cnote.site. The domain stays yours at your registrar; nothing is deleted. You can connect it again any time." Cancel / Disconnect (danger). No typing to confirm (Cloudflare's typed confirm is for a destructive case; this one is reversible).

## 2. Manual editor

**References**
- [Lovable](https://mobbin.com/screens/8b3ae633-ddcf-4dde-822e-22396d9c04ac): the design panel replaces the chat in the same left column, with "Back to Chat" and a small prompt box kept on the selected element. One column, two modes.
- [Framer](https://mobbin.com/screens/ab923887-5ce7-4236-a9c6-d2bd250c046e): one right panel with two tabs, "Agent" and "Style".
- [Workable](https://mobbin.com/screens/883c12a5-c8ae-4852-bdfb-acfbf61b9868): panel titled after the block ("Header section"), grouped fields, image field as thumbnail + "Replace file or drag and drop", plain-language helper text.
- [Squarespace list field](https://mobbin.com/screens/600b81df-a40b-4fdf-bc48-8fae6dd536fd): rows with a red remove dot, drag handle on the right, "Add social link" under the list.
- [Buffer Start Page](https://mobbin.com/screens/a89e0f9e-ed15-4049-bce7-ae86fd7562c4): "Your Site Layout", every block as a row with drag handle, thumbnail, name and a text snippet. [Behance](https://mobbin.com/screens/59697694-c5a7-40d5-8db2-e39010585765) does the same in a "Reorder Content" modal.
- Colour: [Salesforce](https://mobbin.com/screens/1adf99bd-91ac-4a01-ae4f-3d15af2781eb) (swatch + hex). Save state: [Podia](https://mobbin.com/screens/a316a13a-86d7-40ec-9d81-a2d467bcc444) ("✓ Saved" beside Publish).
- Avoid: [Shopify's tree](https://mobbin.com/screens/08e8a8b7-ee0c-4856-ad3b-b3e691ba767b) of sections and nested blocks, and Lovable's margin/padding fields.

**Pattern to copy.** Keep two columns. The left column gets a segmented control `Chat | Edit`; the preview never moves. Side by side (three columns) is rejected: at 1280px the preview drops under 600px and owners face two places to type (inferred). Clicking a block selects it in either mode; switching tabs keeps the selection, so "Editing: Hero" in Chat and the Hero fields in Edit are the same thing. Edit with nothing selected shows the section list (Buffer), which is also where drag-to-reorder lives; the block toolbar's ↑ ↓ stay for touch. Manual edits cost no AI edits and appear in the chat thread as muted change cards ("You changed the Hero heading · Undo") so there is one history (inferred).

```
┌ ← │ Sharma Dental ✎ │ [Home ▾] │ ● Live │ ↶ ↷ │ ✓ Saved    [⏱][⚙] [Publish changes] ┐
├ [ Chat | Edit ]  380px ──────┬ PREVIEW ───────────────────────────────────────────┤
│ ← Sections    Hero           │ ┌ ◦◦◦ sharmadental.in              [🖥] [📱] ┐    │
│ Layout  [▣ Photo right ▾]    │ │┌[Hero]──────────────[↑][↓][⧉][🗑]────────┐│    │
│ Heading                      │ ││ selected block, updates as you type     ││    │
│ [Gentle dental care in Pune] │ │└─────────────────────────────────────────┘│    │
│ Text                  84/160 │ │ Services / About / Contact form / Footer  │    │
│ [Family clinic in Kothrud… ] │ └───────────────────────────────────────────┘    │
│ Photo                        │                                                   │
│ [thumb] Change · Remove      │  EDIT, NOTHING SELECTED (left column)             │
│ Button text  [Book a visit ] │  Sections on Home                                 │
│ Button goes to [Contact ▾]   │  ⠿ Header            (fixed)                      │
│ Colour  (●)(●)(●)(●) Custom  │  ⠿ Hero       Gentle dental care…                 │
│ Services            3 items  │  ⠿ Services   6 services                          │
│ ⠿ Root canal          ✎  ⊖  │  ⠿ About      Dr Sharma has…                      │
│ ⠿ Braces              ✎  ⊖  │  ⠿ Footer            (fixed)                      │
│ + Add service                │  [+ Add section]                                  │
│ ✨ Ask AI to rewrite this    │                                                   │
└──────────────────────────────┴───────────────────────────────────────────────────┘
```

**Field types.** Text: one-line input. Long text: 3-row auto-growing textarea with a soft counter (amber past the limit, never blocking). Image: 64px thumbnail, Change (opens the picker in §6), Remove, and the Phase 1 alt-text line. Colour: four theme swatches first, "Custom" opens swatch + hex (Salesforce); a low-contrast pick shows "Hard to read on this background" (inferred). List: rows with drag handle, title, edit (expands the item's fields in place), remove; "+ Add" under the list; removing shows a 5-second Undo toast instead of a confirm (inferred). Link: a dropdown of pages and sections, plus "Phone", "WhatsApp", "Other link". No spacing, font-size or CSS fields. Changes apply to the preview as typed and autosave after 800ms (inferred).

## 3. Pages

**References**
- [Etsy Pattern](https://mobbin.com/screens/d663fbba-7314-4e93-821f-2b4d2ea3fde5): the whole model in one small panel. "Edit page" list with a tick on the current page and a "Hidden" tag; "Add page" list of named presets (Contact Us, Events, Gallery); [row menu](https://mobbin.com/screens/f07d1ea8-4cdb-43cc-907c-df5c63fa032d) with Hide page and Delete.
- [Mailchimp Add Page](https://mobbin.com/screens/47b32257-5ca1-4288-abf7-1cf831fd77b9): radio list (General, About, Contact, Blank) with the chosen layout previewed live on the right.
- [Squarespace Pages](https://mobbin.com/screens/a3d45959-62e9-451e-9b6d-263b1d9b3602): "Main Navigation" and "Not Linked" groups; the top bar centre reads "Home · Page · Published". [Add a Page](https://mobbin.com/screens/3746080c-51ac-4d23-b970-f6f756b9f997) groups templates by purpose.
- [Gamma](https://mobbin.com/screens/cdaeec1c-4368-4802-89b0-9d9923e09c0e) page menu (Rename page or edit URL path, Page setup, Duplicate, Delete) and its [rename modal](https://mobbin.com/screens/f10a4235-7e48-49c3-88d7-0e6a503707e6): Title 10/70, URL path with a fixed `/`, Description 0/160, share and search previews.

**Pattern to copy.** A page switcher in the top bar after the site name (`[Home ▾]`), because the left column is already taken by Chat/Edit. The dropdown is the page list, the menu order and the add-page entry in one (Etsy). Templates are a named list, not a gallery; each is pre-filled from the business profile. Privacy policy and Terms are generated drafts with a banner "Draft. Please read before publishing" and go to the footer, not the menu (inferred). Home cannot be deleted or hidden. A chat request ("Add an About page") lands in the same place.

```
[Home ▾]                               ADD PAGE (modal)
┌ Pages ─────────────────────────┐     ┌ Add a page ──────────────────────── × ┐
│ IN YOUR MENU   (drag to order) │     │ ( ) About us        ┌ preview ──────┐ │
│ ⠿ Home              ✓       ⋯ │     │ (•) Services        │ rendered with │ │
│ ⠿ Services                  ⋯ │     │ ( ) Contact         │ your details  │ │
│ ⠿ Contact                   ⋯ │     │ ( ) Privacy policy  │               │ │
│ NOT IN MENU                    │     │ ( ) Terms           └───────────────┘ │
│   Privacy policy  (footer)  ⋯ │     │ ( ) Blank page                        │
│ ────────────────────────────── │     │ Page name [Services            ]      │
│ + Add page                     │     │ Address   sharmadental.in/[services ] │
└────────────────────────────────┘     │                  [Cancel] [Add page]  │
 ⋯ → Rename · Page settings ·          └───────────────────────────────────────┘
     Hide from menu · Duplicate · Delete
```
Rename is inline in the row. **Page settings** reuses the Phase 1 "How you appear on Google" group per page: Name shown on Google (60), Short description (155), address after the `/`, live preview. Changing the address of a published page warns "Links you already shared to /services will stop working" (Phase 1 wording). **Delete:** "Delete the Services page?" / "It is removed from your menu and website. You can bring it back from Version history for 30 days." (retention inferred). Limit of 10 pages on the free plan is a placeholder.

## 4. Version history

**References**
- [Wix Post History](https://mobbin.com/screens/30c1385f-3728-4c4d-a282-0f113a506653): list left, full preview right, "Current version" tag, "Edited by", Restore on the selected row.
- [Google AI Studio](https://mobbin.com/screens/26c8f8ba-2aaa-4028-8983-d41f4b71b784): each version is named by the prompt that made it ("Apply style changes to the selected element"); a "Current" pill.
- [Fibery](https://mobbin.com/screens/eb05e8be-4a16-47f0-94fe-08ad3da74171): versions grouped by day, and a confirm that names the target: "Your current version will revert to version Jan 8, 2026 1:13 pm."
- [Substack](https://mobbin.com/screens/b9578385-f96c-45a2-b2e2-483bfc410cb6): time, full date and author on each row; "Restore draft" in the footer.

**Pattern to copy.** The chat's change cards remain the everyday undo. The clock icon in the top bar opens a right sheet (same 480px sheet as Settings). Rows say what changed in the owner's words (the change-card summary), who (You, C Note AI, or a teammate's name) and when, grouped by day; each publish is a labelled milestone. Selecting a row shows that version in the preview frame under a banner; nothing changes until Restore. Restore makes a new version on top, so it can itself be undone (inferred).

```
┌ Version history ───────────────── × ┐   PREVIEW while a row is selected
│ TODAY                                │   ┌ Viewing 23 Nov, 6:10 pm · not your   ┐
│ ● Now                     Current    │   │ live site       [Back to now][Restore]│
│ 2:41 pm  Hero heading changed        │   └───────────────────────────────────────┘
│          You                         │
│ 2:30 pm  Added FAQ section           │   CONFIRM
│          C Note AI                   │   Restore this version?
│ YESTERDAY                            │   Your website goes back to how it was on
│ 6:10 pm  ▲ Published     ← selected  │   23 Nov, 6:10 pm. 4 later changes are
│          You                         │   kept in history. Your live site changes
│ 5:52 pm  Replaced logo               │   only when you publish.
│          Priya (staff)               │            [Cancel] [Restore]
│ Show older                           │
└──────────────────────────────────────┘
```

## 5. Site analytics

**References**
- [Lovable](https://mobbin.com/screens/b9f5f997-18eb-4deb-b70f-278606f1b5ee): number tiles, one area chart, then Source / Page / Country / Device as bar-rows; "Last 7 days".
- [Plausible](https://mobbin.com/screens/8a9141d6-6574-4e69-a33c-94de8a2cfe14): Sources and Top pages as two side-by-side bar lists; first-data banner "Your first pageview has landed!".
- [Squarespace](https://mobbin.com/screens/bc8707aa-636a-4d38-bee0-6e328a9c78f9): each number with a "+100% wk/wk" comparison.
- Avoid: [Hotjar donuts](https://mobbin.com/screens/e5a22b77-d416-4f94-beec-70655979ece7) and Plausible's bounce rate / visit duration tiles.

**Pattern to copy.** One card, three numbers, one chart, two lists. "Visitors", never sessions or pageviews. Sources are translated: Google, WhatsApp, Instagram, Facebook, "Typed your address or scanned your QR" for direct (inferred wording). Enquiries sit next to visitors because that is the number owners care about, and links to Leads (inferred). Fixed 30 days with a 7/30 toggle only; no date picker, no filters. Opened from the status pill menu and the publish Live view.

```
┌ Your website · last 30 days                          [7 days | 30 days] ┐
│ 412 visitors            1,038 pages viewed        9 enquiries  → Leads   │
│ ↑ 18% vs previous 30 days                                                │
│  40┤            ╭╮                                                       │
│  20┤   ╭╮  ╭────╯╰╮    ╭─╮      ╭╮                                       │
│   0┼───╯╰──╯      ╰────╯ ╰──────╯╰────                                   │
│    25 Oct          8 Nov           23 Nov                                │
│ WHERE VISITORS CAME FROM          MOST VIEWED PAGES                      │
│ Google          ▓▓▓▓▓▓▓▓░░  210   Home          ▓▓▓▓▓▓▓▓▓░  520          │
│ WhatsApp        ▓▓▓▓░░░░░░  104   Services      ▓▓▓▓░░░░░░  260          │
│ Typed / QR      ▓▓░░░░░░░░   61   Contact       ▓▓░░░░░░░░  140          │
│ Instagram       ▓░░░░░░░░░   37   About         ▓░░░░░░░░░   88          │
│ We count visits without cookies and don't track individual people.      │
└──────────────────────────────────────────────────────────────────────────┘
```

## 6. Stock photo picker

**References**
- [Partiful](https://mobbin.com/screens/5912733b-bf0f-47dc-ad30-a27b32afa5ab): search on top, suggestion chips, "by {photographer}" under every photo, "Source: Unsplash" label, Upload button in the same modal.
- [Buffer](https://mobbin.com/screens/2b16061e-3230-4bea-95f5-afa6b969f33c): provider name in the title, photographer's name as a link under each photo.
- [Magnific](https://mobbin.com/screens/e849cd9f-2926-4653-92c2-276648544f7a): two tabs, stock and "Uploads", with one Upload button above both.
- [Posh](https://mobbin.com/screens/fe5f187e-401e-4ad2-8b56-8665f121a70a): drop zone with size guidance above a searchable grid.

**Pattern to copy.** One modal, two tabs; it replaces Phase 1's direct file picker for "Change" and "Add a photo". The search box is pre-filled from the block and business ("dental clinic") so the grid is never empty (inferred). Click selects, one "Use this photo" confirms. The credit is stored with the image and shown in the builder; whether it must appear on the published site follows Pexels' terms (to confirm).

```
┌ Choose a photo ──────────────────────────────────────── × ┐
│ [ Free photos | Your photos ]                             │
│ 🔍 [ dental clinic                                    ]   │
│ (clinic) (dentist) (smile) (reception) (team)             │
│ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐                       │
│ │      │ │  ✓   │ │      │ │      │   3 columns on phone  │
│ └──────┘ └──────┘ └──────┘ └──────┘                       │
│ by A. Rao  by M. Chen by …     by …                       │
│ [ Show more ]                                             │
│ Photos provided by Pexels · free to use                   │
│                              [Cancel] [Use this photo]    │
└───────────────────────────────────────────────────────────┘
 Your photos tab: drop zone "Drag a photo here or [Upload]" · JPG, PNG or WebP,
 up to 10 MB · then a grid of photos already uploaded to this site.
 No results: "No photos for 'xyz'. Try a simpler word, or upload your own."
```

## 7. Export and leaving

**References** (no website-builder export screen was found on Mobbin; these are data exports)
- [Etsy](https://mobbin.com/screens/ac957aa3-1029-4840-a473-73bfd528207c): section titled "Take your data with you"; green notice "We're generating your download and will email you a link when it's ready."
- [beehiiv](https://mobbin.com/screens/82fd6355-3f3e-47aa-b15b-6fc56553850c): "Recent Exports" table with date, status pill and a Download link.
- [Slack](https://mobbin.com/screens/a40352d7-dd4b-45d8-8dc4-8400e8563b31): two columns, "Here's what's included" and "What's not included".
- [15Five](https://mobbin.com/screens/36e89227-0e7f-490e-b67c-a9aeb3472565): "Your exports will expire 24 hours from the time of creation."

**Pattern to copy.** A group in Settings called "Your website is yours" holding both exit rights: download and domain disconnect (§1). One button, no format choice. State what is and is not in the file (Slack). Small sites are ready in seconds, so prepare in place and fall back to a notification only if it takes over a minute (inferred).

```
YOUR WEBSITE IS YOURS
Download a copy                                   [Download website]
Included: every page as it looks today, your photos, your text.
Not included: enquiries (export those from Leads); the contact form
needs C Note to work, so it becomes a "Call us" button in the copy.
  preparing: ◌ Preparing your download…  (button disabled)
  ready:     ✓ sharmadental-24-nov.zip · 4.2 MB  [Download] · link works for 24 hours
Own domain: sharmadental.in ● Live                [Disconnect]
```
The form fallback and the ZIP-of-static-pages format are inferred.

## 8. Section layout variants

**References**
- [Squarespace Change Layout](https://mobbin.com/screens/3dbbd806-79bd-4fc5-98d1-a805a9f30de5): thumbnails stacked in the left column, "Selected" on the current one, the real page previewing the choice on the right, Save / Cancel.
- [Workable](https://mobbin.com/screens/4230b5f1-7870-488a-83f0-92a94b83424a): "Header layout" as two radio thumbnails inside the block's own panel.
- [Squarespace Add a Section](https://mobbin.com/screens/71c236f7-da32-442e-a448-44ab139c557f): layout thumbnails rendered in the site's own fonts and colours.

**Pattern to copy.** Layout is the first field of every block in Edit, and a fifth toolbar button `[▣ Layout]` in the preview. It opens a list of 2–4 thumbnails drawn as simple wireframes tinted with `--site-primary` (inferred; real renders are heavier). Clicking one applies it to the preview at once; content is kept, and anything a layout cannot show is hidden, not deleted ("Photo is hidden in this layout"). No Save step: it is a normal change with Undo.

```
┌ Hero layout ─────────────────── × ┐
│ ┌────────┐ ┌────────┐ ┌────────┐  │
│ │▬▬  ▓▓▓ │ │ ▓▓▓▓▓▓ │ │  ▬▬▬▬  │  │
│ │▬   ▓▓▓ │ │  ▬▬▬   │ │  ▬▬    │  │
│ │▭       │ │   ▭    │ │   ▭    │  │
│ └───✓────┘ └────────┘ └────────┘  │
│ Photo right  Full photo  Text only│
└───────────────────────────────────┘
```

## 9. Embed block

**References**
- [Pitch](https://mobbin.com/screens/59579d34-8ccc-4eb0-8291-12150fb61146): provider list on the left (YouTube, Vimeo, Loom), an illustration, one URL field and Add.
- [Wix](https://mobbin.com/screens/5f2fca5a-21b5-48db-9926-487b9937b946): "Paste a URL from YouTube, Vimeo or Facebook" above a single field.
- [PandaDoc](https://mobbin.com/screens/53f08351-04c1-4b04-a601-00cf683042d0): supported providers named in the helper sentence.
- [VEED](https://mobbin.com/screens/2f55ffdd-6905-46ec-a777-44c1eea96ea7): red line "Sorry. This doesn't look like a link we support."
- Avoid: [Behance](https://mobbin.com/screens/cc59ce52-18ca-4dd5-b322-8b89aead4ad9) "Paste Embed Code".

**Pattern to copy.** Add section → "Video, map or booking". One field that takes a normal share link, never embed code. The provider is detected from the link and shown as a chip with a preview before Add. Pasting a supported link into chat does the same thing.

```
┌ Add a video, map or form ─────────────────────── × ┐
│ Paste a link                                        │
│ [ https://youtu.be/Qsq-5j…                       ]  │
│ ✓ YouTube video · "Clinic tour"   [thumbnail]       │
│ Works with: YouTube · Google Maps · Calendly ·      │
│ Google Forms. Other websites can't be added, to     │
│ keep your site safe and fast.                       │
│                                  [Cancel] [Add]     │
└─────────────────────────────────────────────────────┘
 unsupported: ✕ We can't add links from this website yet. [Tell us which one]
 private:     ✕ This video is private. Make it public or unlisted on YouTube.
```
On the published site the embed loads only when it scrolls into view or is tapped (inferred, for speed on mobile data).

## States

| State | Where | What the owner sees |
|---|---|---|
| DNS pending | Domain sheet step 3; Settings | `○ Looking for your records…` "This can take a few minutes, sometimes up to 48 hours. You can close this." Per record: `○ Not found yet`. Pill in Settings: `◌ Connecting` |
| DNS verified | Domain sheet | `✓ Records found` with time; both record cards show `✓ Correct` |
| Certificate pending | Domain sheet | `◌ Securing your site (https)… usually a few minutes`; old address still serves the site |
| Live | Domain sheet, Settings, top bar | `✓ Live at sharmadental.in` + Visit, Copy link, Share on WhatsApp; browser frame shows the new address |
| DNS error, wrong value | Record card | Red card: "Record 1 points to 184.168.x.x. It should be 203.0.113.10. Edit it at GoDaddy and check again." + `[Check again]` |
| DNS error, extra record | Record card | "There are two A records for @. Remove the one pointing to …" |
| DNS error, not found after 48 h | Step 3 | Amber: "Still not found. Check the records were saved, or [Talk to a person]." Checking continues once an hour (inferred) |
| Domain in use | Step 1 | "This domain is connected to another C Note website." |
| Editor unsaved | Top bar | `● Editing…` (grey) while typing; leaving the page is blocked only in this state |
| Editor saving / saved | Top bar | `◌ Saving…` → `✓ Saved`; save failure: red `Not saved · Retry`, fields keep their values |
| Editor conflict | Banner over the left column | "Priya changed this page while you were editing." `[See their version]` `[Keep mine]`; AI edits are queued until resolved (inferred; no reference found) |
| AI applying while in Edit | Edit panel | Fields of the target block are read-only with shimmer until the change card lands (inferred) |
| Version preview | Preview frame | Banner "Viewing 23 Nov, 6:10 pm · not your live site" + Back to now / Restore; chat and Edit disabled |
| Analytics empty | Analytics card | Not published: "Publish your website to see visitors here." Published, no visits: flat chart + "No visitors yet. Share your link to get started." `[Share on WhatsApp]` `[Download QR code]` |
| Analytics first data | Analytics card | One-time line "Your first visitor has arrived" (Plausible) |
| Pexels unavailable | Photo picker | Free photos tab: "Free photos aren't loading right now." `[Try again]` `[Upload your own]`; Your photos tab keeps working |
| Photo search empty | Photo picker | "No photos for '…'. Try a simpler word, or upload your own." |
| Export preparing | Settings | Spinner + "Preparing your download…"; button disabled; safe to close |
| Export ready | Settings, notification | File name, size, `[Download]`, "link works for 24 hours" |
| Export failed | Settings | "Couldn't prepare the download. Your website is unchanged." + Retry |
| Embed unsupported | Embed modal | Red line under the field + "Tell us which one"; Add disabled |
| Page limit reached | Pages dropdown | "+ Add page" replaced by "You've used all 10 pages on the free plan." `[Tell me when it's available]` |
