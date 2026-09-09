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
  screen changed nothing in the frame.
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
  echoes it.
- `type/code` is `16 00` for a menu-bar title; for menu items it varies
  (`Log on` 02 64, `New password` 12 05, `Log off` 12 0f) — the low byte looks
  like the item's function number (New password 0x05, Log off 0x0f), the high
  byte a flag. **Open question:** the exact function-code encoding and what the
  client sends when the item is chosen — needs one targeted sniff (pick a menu
  item, read the resulting OK-code).
- the trailing single letter is the Alt-accelerator (`U`ser, `N`ew password).

`.03`/`.04` reuse the same shell with the code doubled (`New password` → `05 05`,
`Log off` → `0f 0f`) and a tooltip/label after the text.

**Reuse works today:** splicing our fields DYNT_ATOM into a captured logon frame
inherits its real MNUENTRY, so the menu bar, `New password` and status are
genuine (`server` login scene). **Synthesis** (a Go DSL that builds MNUENTRY from
a description — menu bar, dropdowns, context menus, toolbar, keys) is the next
build; it unlocks keyboard control for games and fully native custom screens.

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
