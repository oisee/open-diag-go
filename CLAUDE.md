# CLAUDE.md — open-diag-go-pro

SAP GUI as a display: a server (and now a terminal client) that speaks the
DIAG protocol well enough for a real SAP GUI to draw from our frames.

## Where the knowledge lives

- [`KNOWLEDGE.md`](KNOWLEDGE.md) — the DIAG protocol reference for **this**
  repo (frame/headers, items, DYNT_ATOM, list channel, icons, MNUENTRY, ALV).
  Living, marked confirmed/inferred. Read it before touching `pkg/diag`.
- **Shared knowledge base:** [`../sap-kb/`](../sap-kb/) maps this repo against
  its siblings (vsp, open-rfc-go, sap-sso-trace) — the layer stack, the reuse
  matrix, and the cross-repo backlog.
  - This repo's chapter: [`../sap-kb/repos/open-diag-go-pro.md`](../sap-kb/repos/open-diag-go-pro.md)
  - Cross-repo backlog: [`../sap-kb/backlog.md`](../sap-kb/backlog.md)

## Layout

`pkg/diag` protocol codec · `pkg/frame` the canvas · `pkg/alv` ALV row-blob
codec + the SAP-LZH **writer** (the only encoder in the family; vsp only
decodes) · `pkg/tui` styled renderer (chrome + colour + icons) · `pkg/replay`
capture template/splice · `cmd/tap` proxy · `cmd/lens` decoder · `cmd/lsd`
banner · `cmd/server` demo engine · `cmd/tui` read-only + live-logon terminal.

## Rules

- **Never commit `captures/`.** Real logon, session GUIDs, credentials.
- Depends on sibling checkouts via `replace`: open-rfc-go (`pkg/ni`,
  `pkg/sniffer`), vsp (`pkg/sapcompress`).
- Live DIAG logon works only against the A4H sandbox (SSO systems have no
  plain-DIAG password logon); one logon attempt per run to avoid user lock.
