# Templates with data bindings (ADR-006)

A template is a page or board whose text contains `{{placeholders}}`. Rendering resolves them from a CRM,
billing or projects record and creates a new note.

## Commands

| Command | Purpose |
| --- | --- |
| `template.save {note_id, name, source_kind}` | Copy a page or board into a template. Same name replaces it. |
| `template.list {source_kind?, kind?}` | Templates (templates with no source are always included). |
| `template.delete {template_id}` | Notes already rendered keep their content. |
| `template.fields {source_kind}` | Placeholder catalogue for the editor's insert-field picker. |
| `template.render {template_id, source_id?, space_id?, title?}` | Creates a new note; returns `{note, unresolved}`. |
| `template.refresh {note_id}` | Re-renders a rendered **page** from the record's current data. |

`source_kind` is `none | contact | deal | invoice | item`. The catalogue lives in one place,
`crates/cnote-docs/src/templates.rs` (`catalogue()`): `workspace.name`, `today`, `user.name` everywhere;
`contact.*`, `deal.*` (+ `contact.*` of the linked contact), `invoice.*` (+ `contact.*`), `item.*`.

## Rules

- Plain path lookup only. No expressions, no recursion: values are never re-scanned for placeholders.
- Money is formatted from i64 paise (`₹12,34,567.00`, Indian grouping; other currencies as `USD 1,234.50`).
  Dates are IST (`1 Apr 2026`).
- Unknown placeholders stay visible as written and are listed in `unresolved`. Fields that exist but are empty
  (no email on the contact) resolve to an empty string.
- The caller must be able to read the source note. Linked records (the deal's contact, the invoice's contact)
  are included only if their space is readable too; otherwise those placeholders are unresolved, so a deal in an
  open space never leaks a contact in a private space.
- Boards: the tldraw snapshot JSON is parsed and substituted inside string values, so quotes or newlines in data
  cannot break it. Placeholders must sit in a single text run (a placeholder split across formatting runs is
  not matched).
- Binding (template, source kind/id) is stored in `docs.template_bindings`. `template.refresh` works for pages
  only and overwrites the page body, including manual edits. Boards are one-shot: render again for fresh data.
- Templates are workspace-wide (any non-guest member can list and render them) and copy content at save time.
  Saving a note that lives in a private space shares its text with the workspace.
- Every new workspace gets two starter templates (workspace hook): "Proposal" (deal) and "Invoice cover letter"
  (invoice).

## Web

- Page toolbar: **Field** (insert placeholder at the cursor) and **Save as template**.
- Whiteboard toolbar: **Field** (adds a text shape) and **Save as template**.
- **New from template** on the Docs and Whiteboard home pages; **Use template** on contact, deal and invoice
  detail. If placeholders were unresolved the dialog lists them before opening the note.
- A board rendered from a template carries its snapshot in `content_md`; the board loads it when the Yjs room is
  still empty.
