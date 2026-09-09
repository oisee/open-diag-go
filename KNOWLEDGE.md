# open-diag-go-pro — protocol & element knowledge base

What we have reverse-engineered of the SAP GUI **DIAG** protocol, read off real
captures on an A4H ABAP trial. This is the living reference; the authoritative
detail lives next to the code (`pkg/diag/*.go` comments), and this file is the
map over it. Each fact is marked **confirmed** (seen on ≥2 screens or proven by
sending it to a real GUI) or **inferred** (best reading of one sample).

> Convention: bytes are hex, multi-byte integers big-endian unless noted.
> Never commit captures — they carry a real logon, session GUIDs and
> credentials. Findings here are protocol facts only.

---

## 1. Frame & headers

```
NI frame → [DP header, 200 bytes, client's first frame only] → DIAG header (8 bytes) → items
```

DIAG header (8 bytes): `mode, com/ComFlag, stat, err, type, info, rc, compress`.
Body is compressed (LZC/LZH) — see `pkg/sapcompress`. A bare header with
`ComFlag = 0x0a` (EOC|EOP) and no body is the clean session end (`server`'s
`closeSession`).

## 2. Items

| item | id/sid | length | meaning |
|------|--------|--------|---------|
| APPL | 0x10 | id+sid+2-byte len | the workhorse |
| APPL4 | 0x12 | id+sid+4-byte len | large payloads |
| XML | 0x11 | 4-byte len | DATAMANAGER / control XML |
| SES | — | 16 | session |
| EOM | — | 0 | end of message |
| SBA / SFE / SLC | — | 2 / 3 / 2 | classic-list run header (see §5) |

Sub-streams keyed by APPL id/sid:
- **DYNT_ATOM** = APPL4 id 0x09 sid 0x02 — the screen (§4).
- **VARINFO** = id 0x0c: sid `0x03` status message (§7), `0x04` OK-code /
  function code, `0x0b` list text (§5).
- **MNUENTRY** = APPL4 — the GUI status: menu, toolbar, function keys (§6).
- **UI_EVENT.UI_EVENT_SOURCE** = Control Framework events (§8), *not* keyboard.

## 3. Input model (confirmed)

The DIAG input model is **discrete**, one PAI (one round-trip) per action — no
per-keystroke stream, no key-held.

- A **pushbutton** click sends its function code as `=FCODE` in the OK-code
  field (**VARINFO id 0x0c sid 0x04**). Confirmed: `=YES`, `=NO`, `=SHOP`,
  `=FL` seen in a capture. System commands ride the same field: `/i` (close),
  `/n`, `se38`. Cursor position arrives here too as `%_GC <row> <col>`.
- **Enter** always fires a (usually empty-fcode) PAI.
- **Function keys / menu shortcuts** fire a PAI **only if the screen's GUI
  status maps them** (§6). Without a status they produce a contentless PAI —
  proven with the `echo` mode: pressing F-keys / PgUp / PgDn on a status-less
  screen changed nothing in the frame. A second sniff confirmed it: pressing
  keys on standard screens sent mostly cursor/scroll (`%_GS 0 1`, `%_GC r c`)
  and system commands, not function codes — **only a registered function sends
  a `=STRING`.** How a key is registered so a chosen string is sent is §8's
  open thread.
- **Arrow keys, letters, mouse-move** are not sent per press — arrows move the
  cursor locally; letters go into fields and travel with the next PAI.

Consequence: turn-based games (each keypress = one move) fit directly; a
real-time feel needs a server timer (which the GUI accepts, §9) plus discrete
key/button PAIs.

## 4. DYNT_ATOM — the screen (see `pkg/diag/dynt.go`)

A chain of atoms, each: 12-byte header `[len BE][2 flags][etype][area][block]
[group][row BE][col BE]` (row/col 0-based) then an etype-specific tail.

Etypes: `0x72` field-name, `0x73` pushbutton, `0x78` xmlprop, `0x7f` frame,
`0x80` checkbox, `0x81` radio, `0x82` input, `0x83` output, `0x84` label.
Pushbutton and frame carry a `Height` (in cells) — buttons and boxes can be
more than one row tall. Attr bits: Protected 0x01, Invisible 0x02, Intensify
0x04, JustRight 0x08, Matchcode(F4) 0x10, PropFont 0x20, Yes3D 0x40,
ComboStyle 0x80.

Field width/height limits (from the encoder): pushbutton width and height are
**1 byte each → max 255**; frame width/height are 2 bytes; field visible length
1 byte (255), max-chars 2 bytes. Positioning is character-cell only — no pixels
for dynpro elements (pixels live only in Control Framework containers).

## 5. Classic list channel (see `pkg/diag/list.go`)

Not a DYNT screen — a stream of positioned coloured runs. Per run, in order:
`SBA(row,col)` → `SFE(byte0 text/ruled, byte1 colour, byte2)` → `SLC(len BE)`
→ `VARINFO.0b(text)`. Colours (SFE byte1) match ABAP `FORMAT COLOR`: Off 0x00,
Heading 0x01, Normal 0x02, Total 0x03, Key 0x04, Positive 0x05, Negative 0x06,
Group 0x07. A ruled `ULINE` run has SFE byte0 = 0x08.

**The list channel accepts server pushes on a timer** (confirmed: the
`iconanim` mode animates a list) — animation is not dynpro-only.

## 6. Icons

An icon is the four ASCII bytes **`@XX@`** inside an ordinary text value; the
GUI substitutes the bitmap client-side. Confirmed on the wire: the same icon
written `AS ICON`, as a raw constant, and as a typed `'@0A@'` string all arrive
as identical bytes (`40 30 41 40`) with the plain-text SFE — `AS ICON` changes
nothing on the wire.

**Where `@XX@` renders depends on the element, not the token:**
- classic-list `VARINFO.0b` run — **yes** (a colour stream of little pictures);
- dynpro **output field** (`0x83`) — **yes** (the logon welcome lines are
  output fields starting with `@0S@`, and the GUI draws the info icon);
- dynpro **label** (`0x84`) — **no**, the token prints literally.

Icon-with-tooltip form (seen on status buttons): `@XX\Qtooltip@caption`.

Confirmed codes: `@08@` green light, `@09@` yellow, `@0A@` red, `@5B@` led-green,
`@01@` checked, `@0V@` okay, `@0W@` cancel, `@0S@` info, `@0Y@` create,
`@0Z@` change, `@10@` display, `@6C@`/`@6A@` menu icons.

## 7. Status message & sound

A status message = **VARINFO id 0x0c sid 0x03**: byte0 = type (`S` success,
`W` warning, `E` error, `I` info), then padding, then the text. The type byte
makes the GUI play its Success/Warning/Error `.wav`, which can be swapped for a
custom track (`…\SAPGUI\Sounds\<scheme>\`). `server`'s `withSound` inserts one
before EOM to score an animation.

## 8. GUI status — MNUENTRY (dissected, encoder pending)

The menu bar, dropdown menus, application toolbar and function keys travel as
**MNUENTRY.01–04** items (APPL4). This is what a key must be declared in before
the GUI will send its function code (§3).

- **.01** menu bar — the top-level titles (logon: `User`, `System`, `Help`).
- **.02** dropdown tree — the menu items (logon `User` menu: `Log on`,
  `New password`, `Log off`; `System` and `Help` menus follow).
- **.03** application toolbar — buttons (text + tooltip).
- **.04** function keys — key position → function + label.

Entry layout (confirmed across .01/.02, one entry):

```
[2 len BE][2 pos: menu#, item#][2 flags=0000][2 type/code][2 pos-repeat]
[2 flags=0000][8 zero][text NUL][1 accelerator letter][pad to len]
```

- `pos` `(menu#, item#)` places the entry in the hierarchy; `pos-repeat`
  echoes it in .01/.02, or holds `[fcode-number][00]` in the flat .03/.04.
- `type/code` = `[flag byte][fcode-number]`. **Low byte = the function number**,
  a small integer that is the *join key* across .02/.03/.04 — or the sentinel
  `0x64` (100) for a menu-only item with no toolbar/key binding. **High byte =
  a flag bitfield**: `0x16` menu-bar title, `0x08` separator, `0x02` enabled
  leaf, `0x12` enabled + bound to an F-key/toolbar slot (bit `0x10` = "has a
  binding"). The doubled code in .03/.04 (`05 05`, `0f 0f`) is not a separate
  rule — the number sits in `type/code` low byte and `pos-repeat` high byte,
  and in the flat lists those two land adjacent.
- the trailing single letter is the **Alt-accelerator** (the mnemonic to open
  the menu), unrelated to the fcode-number.

`.03` (toolbar): `pos.byte0` = toolbar slot; text = `@icon@ caption \0\0 tooltip`.
`.04` (all bound functions): `pos.byte0` = running index; the function-number
is an **opaque function id**, *not* a key code (num 74 = "New GUI Window",
num 80 = "First Page"). It is only the join key; the **keystroke** a function
binds to is defined separately (below).

**Registration — how a key comes to fire (the real mechanism).** MNUENTRY gives
each function a *number* + label + placement. The *keystroke* is bound in a
second table:
- **`ST_R3INFO.13`** (id 0x06 sid 0x13, labelled "TRANSACTION" but carrying the
  **keyboard accelerator table** on a status frame) — rows `[slot][2-ASCII-digit
  function#][NUL]<&keystroke tokens>[NUL]`, e.g. `03 → &F3 & &A&L` (F3 + Alt-←),
  `11 → &C S & &F11` (Ctrl+S / F11), `74 → &C N` (Ctrl+N). A function may have a
  primary and a secondary keystroke (the slot byte).
- **`ST_R3INFO.14`** (`ACCEL_LEGEND`) — the token legend: `&0`=Enter, `&1`=PgUp,
  `&C`=Ctrl, `&S`=Shift, `&F`=F, `&L`=←, … (display data for the menu hint).

So a full registration = an MNUENTRY.02/03/04 entry (number+label) **joined by
the number** to a `.13` row (number→keystroke). **No OK-code string is ever sent
to the GUI** — on a keypress the GUI transmits the *number*, and the 4-char
OK-code string lives only in the ABAP PF-status, resolved server-side.

**How a function reaches the server:**
- **Dynpro elements** carry their `=STRING` *inline in the atom* — a pushbutton
  atom's `Function` = `"=SHOP"`, so a synthesized button sends exactly that on
  click, no MNUENTRY needed (this is how `snake`'s buttons work). Tabstrip tabs
  the same (`DYNT.TABSTRIP_DEF` 0x09/0x0f, `DYNT.TABSTRIP_TAB` 0x09/0x10).
- **Menu / toolbar / function keys** send the MNUENTRY **number**, not a string.

**What we can synthesize:**
- **Pointer-driven (pushbuttons, tabs) — arbitrary string, today.** The `=CODE`
  is authored in the atom (`dynt_encode.go`); full control.
- **Keyboard (F-keys) — a number we own.** We *are* the server, so we register
  a key by emitting an MNUENTRY.04 entry with a function number N of our choice
  **plus** a `ST_R3INFO.13` row binding N to the keystroke (e.g. `&F5`); on F5
  the GUI sends N and we decode N→our action ("up"). We cannot make it emit a
  chosen `=STRING` (no such wire field), but we don't need one.

**Open gap (narrowed):** which C→S item carries the fired number N was never
captured (no bare menu/key press in the capture — SE38 went through the TextEdit
control). One sniff closes it: on Easy Access, press **F8**/**F11** or click a
dropdown item with nothing else focused, and read that single C→S frame.

**Reuse works today:** splicing our fields DYNT_ATOM into a captured logon frame
inherits its real MNUENTRY, so the menu bar, `New password` and status are
genuine (`server` login scene).

## 9. Animation & pacing

Server-pushed frames on a timer are accepted in both channels (dynpro and
list). A full-screen dynpro frame (~6 KB) overruns the GUI below ~60 ms
(freeze); floor the cadence at 60 ms, or push **light** frames (a few widgets /
icons) faster. A frame whose FRAME atom runs off the grid makes a tall,
scrollable, slow-to-repaint canvas — `pkg/frame` now clips boxes to the screen.

## 10. Windows & sessions

- `/i` (OK-code) = window close → answer, then end with the EOP header (§1);
  `server` stops any push loop first so it does not draw over the log-off popup.
- A new session/window is a new `mode` byte in the header (seen from Ctrl+N).
- `/o` opens a new window (used by `server` to start an animation).

## 12. ALV grid & the data channel (Control Framework)

An **ALV grid** is not a dynpro screen — it is a Control Framework control plus a
data channel. Read off an SE16 T100 run (200 rows), driven both as "Standard
list" and "ALV Grid".

**Control setup** (one S→C frame): `CONTAINER.06` = three NUL-terminated
strings `"GRID1\0SAPLSLVC_FULLSCREEN\00500\0"` (control name, hosting repo
program, dynpro); `CONTROL.CONTROL_PROPERTIES` (id 0x0e) = a `[2-byte tag]
[value]` TLV run (id/name/type + geometry); `DYNT.12` = the SHELLID as ASCII
(e.g. "121"); `CONTAINER.01/08/09/0a` = the dynpro↔control geometry handshake
(both directions). An empty `<DATAMANAGER/>` XML rides along on setup.

**Column catalog & focused cell** — in the **DATAMANAGER XML** (`encoding="sap*"`):
`<DATACHANGES HANDLE="1"><IT IDX=col C1=pos C2=width OP="I"/></DATACHANGES>` is
the column catalog (one `<IT>` per column); `<CONTROL SHELLID=..>` `<PROPERTY>`
pairs carry the focused cell in cleartext (`CurrentCellColID`, `CurrentCellText`,
`CurrentCellRowID`). Only the cursor cell is cleartext here, not the whole grid.

**Bulk rows** — in `APPL RFC_TR` (id 0x08), *not* cleartext. The result window
is shipped as an **OLE-automation call to the frontend** (`SAPLOLEA`,
`OLE_FLUSH_CALL`, `IMPORT_XML(XML_DATA_STREAM)`); the row payload is an inner
compressed/serialized blob (250-byte `03 05 …` chunks) inside the already-LZ-
decompressed DIAG body — opaque on the wire in ALV mode. RFC_TR sids: `.00` S→C
(the automation call = grid data), `.01` C→S (results + frontend verb catalog),
`.04` control setup+ack, `.06` `SAPGUI_PROGRESS_INDICATOR` (progress while the
query runs).

**Paging** — client sends `<DATAREQUEST HANDLE="3" NLINES=n FIRSTLINE=m/>` in
its DATAMANAGER XML ("send n rows from m"); server answers with a new `RFC_TR.00`
block. Non-ALV table controls instead page with `APPL4 DYNT.TABLE_ROW_DAT`
(id 0x09 sid 0x05), a 4-byte `[row# BE][flag]` record.

**SADL / IDA** — the IDA stack is named in the RFC_TR string pools
(`CL_SALV_GUI_TABLE_IDA`, `CL_SALV_GUI_GRID_CONTROLER_IDACP`,
`CL_ALV_CUL_CONTROLLER`) but **no OData/SADL query text is on the DIAG wire** —
pushdown is entirely server-side (ABAP↔HANA). To a DIAG client, IDA is
indistinguishable from classic ALV except by these class names; the client only
ever speaks the generic CFW `DATAREQUEST`/automation protocol. The classic list
channel (§5) is the *cleartext* data path; the ALV blob is opaque **but not
unreadable — it decompresses** (below).

**Cracking the ALV row blob.** The `RFC_TR.00` OLE-automation payload is a
**doubly-nested SAP-LZH container fragmented into 250-byte RFC rows**, and it
**decompresses to the real cell values** (proven on T100). To read it:
1. Take the `APPL RFC_TR` (0x08) value; `SplitRFCTR` names `SAPLOLEA /
   OLE_FLUSH_CALL / IMPORT_XML`, params incl. `XML_DATA_STREAM`.
2. Scan for `12 1f 9d` — the SAP-LZH magic; the 8-byte header starts 5 bytes
   before it (`[ulen u32 LE][0x12][1f 9d][x]`, `0x12` = LZH).
3. **De-chunk:** keep the 8-byte header, then strip each 6-byte RFC-row marker
   `03 05 03 05 <len u16 BE>` (`00 fa` = 250) and concat payloads until the tag
   changes (`03 05 03 06 00 00`). Then `sapcompress.Decompress` (LZH = 2-bit
   prefix + raw DEFLATE; already in vsp `pkg/sapcompress`).
4. The decompressed stream is the automation call: a **VERBS script**
   (`CreateControl, SetDataFromUrl, InsertPacket, GetTable …`), a value pool
   (`SAPGUI.GridViewCtrl.1`, `SAP.DataPOnDemand.1`, the row count), and a
   **VARS stream that holds *more* `12 1f 9d` LZH blobs** (not chunked) — the
   DataProvider R3TABLE packets.
5. Decompress those: a **field catalog** (30-char space-padded (struct,field)
   name pairs + a type/len descriptor — for T100: `SPRSL, ARBGB, MSGNR, TEXT`)
   and **data packets** (`ff ff ff ff 01 …` then row-major fixed-width cells).
   Slice cells by the catalog field lengths.

Proof (public T100 demo content, read off the wire): `SPRSL=E ARBGB=Q6
MSGNR=001 TEXT="Enter an existing info structure"` and other varied rows across
many frames. **Caveat:** the provider is `SAP.DataPOnDemand.1` — each flush
ships ~one populated row + catalog + index tables; the rest arrive on scroll
(`<DATAREQUEST>` → new `RFC_TR.00` `InsertPacket`s). So a full table read needs
to drive the paging, not just one frame.

## 13. Control Framework items (grids, trees, editors)

CFW is the pixel-control world. **Its binary structs are little-endian** — the
exception to this doc's big-endian default.

**`UI_EVENT.UI_EVENT_SOURCE` (id 0x0f sid 0x01)** — a 16-byte (or 32-byte, a
second empty slot appended) event *source/focus* descriptor, read as 8×u16 LE:
`w0` event-class, `w1` src-kind, `w2` control-type/subcode, `w3/w4` zero,
`w5/w6` two payload indices (item/row, sub/col — unconfirmed), `w7` trailer
(0x0001 / 0x0101). It names the *source*, not the event — the **semantic event
is in the `<EVENTS>` XML** of the same C→S frame. The two are largely
mutually exclusive; only the TextEdit set-cursor fires both (then the binary
record is a constant `w1=0x0d w2=0x0b` signature with zero payload).

**`CONTAINER.xx` (id 0x0a)** — the container hierarchy. sid is a per-screen
handle. `.06` = **name registration**, three NUL-terminated strings
`<control-name>\0<program>\0<dynpro>\0` (e.g. `GRID1\0SAPLSLVC_FULLSCREEN\00500`,
`EDITOR\0SAPLS38E\00500`, empty name = screen root). Every other sid = a 9-byte
descriptor `A(u16) B(u16) C(u16) D(u16) E(u8)`: `A` = this handle, `C` = parent
handle (confirmed by a tabstrip/sub pair), `D`/`E` = geometry (== the control's
CONTROL_PROPERTIES tag07/tag06). `.01` all-zero = root/desktop.

**`CONTROL.CONTROL_PROPERTIES` (id 0x0e sid 0x01)** — TLV, entries `00 <tag:u8>
<ASCII value> 00`: tag01 ordinal, tag02 control name (space-padded ~32),
tag03/04/05 constant `1/0/0` (flags?), tag06/tag07 = cell geometry that tracks
window resizes. (`CONTROL_FOCUS` sid 0x02 / `CONTROL_EVENT` sid 0x03 exist in
`names.go` but did not occur in the capture.)

**`<DATAMANAGER>` XML (item type XML, 0x11)** — the real CFW payload is C→S with
`encoding="sap*"`; S→C is a 53-byte empty `<DATAMANAGER/>` poll/ack. Children:
- `<COPY><GUI><METRICS .../><DIMENSIONS X0=cols Y0=rows/>` — window metrics at
  logon/resize.
- **`<EVENTS><EVENT SHELLID EVENTID [SHELLEVENT="X"]><PARAM PID VALUE/>…>`** — the
  semantic control-event channel. `SHELLID` = control instance. EVENTID map
  (partial): **12** TextEdit set-cursor/dbl-click (PARAM0 = line text, PID1 line,
  PID2 col), **14** toolbar/function → **PARAM0 = the function code** (e.g.
  `WB_ACTIVATE`), **18/25/36** tree/toolbar (PARAM0 = a right-justified node id).
  So a control's toolbar button sends its fcode here, in the EVENTS XML — the
  control-world analogue of the OK-code (§3).
- `<CONTROLS><CONTROL SHELLID><PROPERTY VALUE NAME/></CONTROL>` — control state
  reported back (TextEdit SelPos*/FirstVisibleLine; ALV CurrentCell*/FirstVisibleRow).
- `<TABLES>` — the ALV/table data channel (§12): `<DATACHANGES HANDLE><IT IDX C1
  C2 OP>` batches and `<DATAREQUEST HANDLE NLINES FIRSTLINE/>` paging.

## 11. Open threads

- **MNUENTRY synthesis** — a Go builder for menus/toolbar/function keys, and
  the function-code encoding (§8 open question).
- **Keyboard for games** — declare F-keys in a synthesized/borrowed status so
  they send `=fcode`.
- **ALV IDA / SADL** (`abap/zodgp_ida.prog.abap`) — how the ALV-with-Integrated-
  Data-Access grid asks HANA for data over DIAG (Control Framework + a data
  channel). Sniff pending; the question is whether the SADL data path can be
  spoken to directly.
- **Control Framework** — the `UI_EVENT`/`CONTAINER`/`CONTROL` items (§8 events,
  grids, trees) — the pixel-control world we have not yet decoded.
