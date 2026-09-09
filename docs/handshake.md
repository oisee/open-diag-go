# DIAG logon handshake, frame by frame

What a server must send, from the client's first frame until the SAP GUI draws a
normal screen. Read off `captures/probe.jsonl`, connection 1, with `lens`. Every
concrete value is redacted here to its meaning and length: nothing that ties the
bytes to a system, a host, a user, or a session is reproduced.

The unit below is one NI payload. Each carries an 8-byte DIAG header
(`mode com stat err type info rc compress`) and then a list of items. The
client's *first* payload also carries a 200-byte dispatcher (DP) header ahead of
the DIAG header; no later frame does, and no server frame ever does.

Item shorthand: `APPL id.SID` is a type-0x10 item (2-byte length), `APPL4
id.SID` is type-0x12 (4-byte length), `XML` is type-0x11 (4-byte length),
`SES`/`EOM` are fixed-length items (16 and 0 bytes). The id/sid names are
protocol lore (pysap/SAP) and are inferred, not confirmed against behaviour; a
few are mislabelled relative to their payload (the item the map calls `SYSID`
carries a codepage number, the one it calls `CODEPAGE` carries the system id).
The structure — types, ids, lengths, order — is what matters and is confirmed.

## The environment block (every server frame begins with it)

Every S->C frame, large or small, opens with the same run of `ST_R3INFO` items
followed by `ST_USER.26`, `ST_R3INFO.0c` and `SES`. This block is mandatory: it
is present in all 24 server frames of the capture. Identifying members are
marked (redact / placeholder in any reimplementation).

| item | id/sid | len | meaning |
|------|--------|-----|---------|
| `APPL ST_R3INFO.CODEPAGE_APP` | 06/11 | 32 | app-server identity blob — **identifying** |
| `APPL ST_R3INFO.SYSNAME` | 06/23 | 16 | **identifying** |
| `APPL ST_R3INFO.SYSID` | 06/24 | 5 | codepage number (e.g. UTF-8), NUL-padded |
| `APPL ST_R3INFO.DBNAME` | 06/21 | 32 | DB/instance GUID — **identifying (DBNAME GUID)** |
| `APPL ST_R3INFO.CODEPAGE` | 06/02 | 3 | 3-letter system id — **identifying (sysid)** |
| `APPL ST_R3INFO.FLOATFORMAT` | 06/03 | 10 | app host name — **identifying (host)** |
| `APPL ST_R3INFO.19` | 06/19 | 2 | flags |
| `APPL ST_R3INFO.01` | 06/01 | 2 | flags |
| `APPL ST_R3INFO.USERNAME` | 06/0a | 2 | (a short code here, not the user name) |
| `APPL ST_R3INFO.GUI_LABEL` | 06/1f | 18–35 | GUID/label blob(s) with an embedded host address — **identifying** |
| `APPL ST_R3INFO.18` | 06/18 | 2 | flags |
| `APPL ST_R3INFO.CPUNAME` | 06/22 | 4 | number |
| `APPL ST_R3INFO.25` | 06/25 | 10 | instance/host token — **identifying** |
| `APPL ST_R3INFO.2d` | 06/2d | 8 | profile name ("Default") |
| `APPL ST_R3INFO.29` | 06/29 | 13 | release / kernel numbers |
| `APPL ST_R3INFO.SESSIONICON` | 06/16 | 4 | number |
| `APPL ST_USER.26` | 04/26 | 4 | internal-mode / roundtrip counter |
| `APPL ST_R3INFO.0c` | 06/0c | 3 | logon client (e.g. "001") |
| `SES` | — | 16 | session/mode state token (see below) |

The `SES` item is the session's running state: 16 opaque bytes the server sets
and the client echoes back on its next frame, with counters inside that change
every roundtrip. It is mandatory in every frame but its content is opaque — a
placeholder of the right length is enough to draw a screen, but a real dialog
that continues past one screen has to carry the value forward.

## Frame by frame

### 1. C->S #0 — client hello (317 B)
`mode=00 com=10 stat=00 err=00 type=00 info=00 rc=00 compress=0`, **200-byte DP
header** present. `com=10` is the INI (initialize) bit; it appears only on this
first client frame — every later frame is `com=00`.

Items (no `SES`, no `EOM` on this frame):
- `APPL ST_USER.CONNECT` 04/02, 12 B — window/geometry + protocol version numbers
- `APPL ST_USER.RFC_PARENT_UUID` 04/0b, 32 B — **identifying (GUID)**
- `APPL ST_USER.GUI_SESSION_UUID` 04/0d, 16 B — **identifying (GUID)**
- `APPL ST_USER.SUPPORTDATA` 04/04, 8 B — capability bitmap
- `APPL ST_USER.17` 04/17, 2 B
- `APPL ST_USER.16` 04/16, 2 B
- `APPL ST_USER.27` 04/27, 2 B — language ("EN")

### 2. S->C #0 — the logon screen (2157 B on the wire, 5406 B decompressed)
`info=01 compress=1`. This is the first drawable screen; answering the hello
with just this frame already makes the GUI paint the logon dynpro.

Order: the environment block above, then the screen payload —
- `SES` 16, `APPL VARINFO.07` 0c/07 16 — window/variant info
- `APPL ST_USER.1a/1b/1c` — screen-state numbers
- `APPL VARINFO.0a` 0c/0a 20 — window title text ("SAP R/3 (1) <sysid>")
- `APPL ST_R3INFO.0f/10/0d/0e/2e` — screen flags
- `APPL4 MNUENTRY.01` 0b/01 86, `.02` 0b/02 551, `.03` 0b/03 47, `.04` 0b/04 65 — the menu / GUI-status / pushbutton texts (Log on, New password, Log off …)
- `APPL VARINFO.09` 0c/09 3 ("SAP"), `APPL DYNN.01` 05/01 22 — the dynpro (screen) descriptor: program, screen number, sizes
- `APPL VARINFO.06` 0c/06 33, `APPL CONTAINER.01/04/05/06` — layout containers
- `APPL4 DYNT.DYNT_ATOM` 09/02 (≈900–1100 B) — **the screen fields ("atoms")**: labels, input fields, their row/col/length. This is what actually draws (Client / User / Password / Language, the title art)
- `APPL DYNT.0b` 09/0b 10 — dynpro trailer
- `XML` 53 — the data manager: `<?xml version="1.0" encoding="utf-16"?><DATAMANAGER/>` (constant, non-identifying)
- `APPL4 ST_USER.18` 04/18 — session blob
- `EOM` — end of message (mandatory terminator)

Mandatory to draw *a* screen: the environment block, `SES`, one `DYNN`, one
`DYNT.DYNT_ATOM`, the `XML` DATAMANAGER, and `EOM`. `MNUENTRY`, `VARINFO`,
`CONTAINER` and the `ST_USER`/`ST_R3INFO` flag items are informational — they
fill in menus, titles and layout; the GUI tolerates their absence far better
than a missing dynpro or a missing `EOM`.

### 3. C->S #1 — logon submit (467 B, `info=00`)
Env-ish echo (`ST_USER.26`, `SES`, `ST_R3INFO.SYSNAME`), the user's field values
in `ST_USER` items (04/24, 04/28, 04/1f …) and a `DYNT.DYNT_ATOM` that carries
the typed field contents — **the user name and the obfuscated password live
here; never printed or copied.** Ends with `XML` + `EOM`.

### 4. S->C #1 — post-logon render (977 B, `info=01`)
Same shape as frame 2: full environment block, `SES`, `DYNN`, `DYNT`,
`MNUENTRY`, `VARINFO`, `XML`, `EOM`. A copyright/licence popup or the first
post-logon dynpro. Also the first frame to carry `APPL ST_R3INFO.CLIENT` (06/0b)
and `VARINFO.03`.

### 5. C->S #2 — continue (189 B, `info=00`)
The user dismisses the popup. `ST_USER` block + `DYNN` + `DYNT.DYNT_ATOM` +
`XML`. `SES` echoed.

### 6. S->C #2–#5 — four small status frames, back to back
`info=02 compress=0`, 308 / 325 / 308 / 291 B, sent in a burst before the next
big frame. Each is **only** the environment block + `SES` + `APPL ST_R3INFO.15`
(06/15, **0-length flag** — the marker that distinguishes an `info=02` frame) +
`EOM`. #2 carries a little extra (`CONTAINER`, `DYNN`, `XML`) — a partial
update; #3/#4/#5 are the bare env+SES+15+EOM minimum.

This is the "small 308-byte frame, then a big one" pattern: `info=02` frames are
lightweight, uncompressed status/keep-busy notifications the server flushes
while it prepares the real screen. They do **not** redraw the dynpro (no `DYNT`
atoms). The drawable screen is the `info=01` frame that follows.

### 7. S->C #6 — SAP Easy Access (3471 B, `info=01`)
Full environment block + `SES` + `APPL RFC_TR.00` (08/00). The appearance of
`RFC_TR` marks the start of the control/RFC roundtrips (`RFC_TR.01` request /
`RFC_TR.00`/`.04`/`.06` response) that stream the Easy Access menu tree and,
later, ALV/control data. From here the flow is dynpro renders (`info=01`)
interleaved with `RFC_TR` control exchanges and occasional `info=02` bursts.

### 8. C->S #3 — first control roundtrip (1055 B, `info=00`)
`ST_USER.26`, `APPL RFC_TR.01` (08/01, the control request), `DYNN`, `DYNT`,
`SES`. The server answers with S->C #7 (`info=01`, `RFC_TR.00`).

## Header fields that move

- `com`: `10` (INI) on the client's first frame only; `00` everywhere else.
- `info`: client frames `00`. Server frames `01` for a full/drawable render,
  `02` for the small uncompressed status frames (paired with the 0-length
  `ST_R3INFO.15` flag). No other `info` value appears in the logon stretch.
- `compress`: server `info=01` frames are LZ-compressed (`compress=1`); the
  `info=02` status frames are usually uncompressed (`compress=0`), and so are a
  couple of very large `info=01` frames near the 30000-byte NI cap. DIAG accepts
  `compress=0`, so a server may send everything uncompressed.
- `mode`, `stat`, `err`, `rc`: `00` throughout this stretch.

## Minimal rogue server

To make a real GUI connect and draw one screen: read the client's hello (frame
1), then send **one** `info=01`, `compress=0` frame consisting of the
environment block, a `SES`, one `DYNN`, one `DYNT.DYNT_ATOM` describing a field
or two, the `XML` DATAMANAGER document, and `EOM`. The identifying members of
the environment block can be placeholders of the observed lengths — the GUI
draws from the dynpro and atoms, not from the system identity.
