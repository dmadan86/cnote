# Support: email to ticket and the customer portal

## Email to ticket

Turn it on under CRM, Tickets, Settings, "Email and portal" (or run `ticket_inbound.rotate` with a `space_id`).
You get an endpoint, `POST /api/public/ticket-mail/<workspace id>`, and a signing secret. The secret is shown once;
rotating it replaces it at once. It is stored encrypted with `CNOTE_MASTER_KEY`.

One request per email, JSON body:

```json
{"from": "Ravi <ravi@example.com>", "to": "support@yourco.example", "subject": "Cannot log in",
 "text": "...", "message_id": "<abc@mail.example.com>", "in_reply_to": "<...>", "references": "<...> <...>"}
```

`references` may be a string or a list. Only `text` is read: HTML is never stored or rendered. The body is at most
256 KB; text is cut to 20,000 characters and the subject to 200.

Headers:

- `X-Cnote-Timestamp`: unix seconds. Requests more than 5 minutes off are refused (401).
- `X-Cnote-Signature`: hex `HMAC-SHA256(secret, timestamp + "." + body)` over the exact bytes you send.

Behaviour: the sender is matched to a CRM contact by email (or created). A reply joins its ticket when it quotes a
`message_id` we hold in `in_reply_to`/`references`, or carries `TKT-000123` in the subject, and only when it comes from
that ticket's contact; anything else opens a new ticket. A resolved or closed ticket reopens. The same `message_id`
twice is stored once (the second answer has `"duplicate": true`). Quoted history ("On ... wrote:", "> ..." lines) is
cut when something remains above it.

## Provider adapters

Providers post their own shapes, so put a tiny adapter between them and the endpoint (a serverless function, a small
route in your own service). It maps fields, signs, and forwards. No SDK is needed. Read the secret from an environment
variable; never commit it.

```js
import { createHmac } from 'node:crypto'

// map(provider payload) -> our shape. Fields per provider:
const maps = {
  // Postmark inbound webhook (JSON): From, To, Subject, TextBody, MessageID, Headers[{Name,Value}]
  postmark: (p) => ({ from: p.From, to: p.To, subject: p.Subject, text: p.TextBody, message_id: p.MessageID,
    in_reply_to: hdr(p, 'In-Reply-To'), references: hdr(p, 'References') }),
  // Mailgun routes (forwarded form fields): sender/from, recipient, subject, stripped-text, Message-Id, In-Reply-To, References
  mailgun: (f) => ({ from: f.from, to: f.recipient, subject: f.subject, text: f['stripped-text'] || f['body-plain'],
    message_id: f['Message-Id'], in_reply_to: f['In-Reply-To'], references: f.References }),
  // SendGrid Inbound Parse (multipart fields): from, to, subject, text, headers (raw block: read Message-ID etc. from it)
  sendgrid: (f) => ({ from: f.from, to: f.to, subject: f.subject, text: f.text,
    message_id: raw(f.headers, 'Message-ID'), in_reply_to: raw(f.headers, 'In-Reply-To'), references: raw(f.headers, 'References') }),
}
const hdr = (p, n) => p.Headers?.find((h) => h.Name.toLowerCase() === n.toLowerCase())?.Value
const raw = (h = '', n) => h.match(new RegExp(`^${n}:\\s*(.+)$`, 'im'))?.[1]

export async function forward(provider, payload) {
  const body = JSON.stringify(maps[provider](payload))
  const ts = String(Math.floor(Date.now() / 1000))
  const sig = createHmac('sha256', process.env.CNOTE_INBOUND_SECRET).update(`${ts}.${body}`).digest('hex')
  return fetch(process.env.CNOTE_INBOUND_URL, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-cnote-timestamp': ts, 'x-cnote-signature': sig },
    body,
  })
}
```

Check each provider's current payload field names before relying on the mapping above; they are the common ones, not a
contract. Make your adapter answer the provider 2xx only after the forward returns 2xx so it retries on failure
(redeliveries are safe: they are deduplicated by `message_id`).

## Replies to the customer

`ticket.reply` (the "Public reply" mode on the ticket page) emails the contact over `SMTP_URL`/`EMAIL_FROM` with
`[TKT-000123]` in the subject and stores the message. Internal notes are ordinary comments and are never emailed or
shown to customers. Set `PUBLIC_WEB_URL` so emailed portal links point at your site.

## Customer portal

Enable it in the same settings section. Customers open `/portal/<workspace id>`, enter their email and receive a
single-use link that is valid for 15 minutes (3 links per contact per 15 minutes; 10 requests per address per 10
minutes). The link starts a 12-hour session in an HttpOnly cookie. Only the SHA-256 of links and sessions is stored.
The portal lists that contact's tickets with customer-visible messages only and offers a reply box. There are no
customer passwords. Turning the portal off ends all sessions.
