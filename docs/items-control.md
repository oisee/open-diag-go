# Control framework on the wire — containers, controls, RFC_TR

This is what the ALV capture (SE16 showing table T100 through an ALV grid:
open, sort, scroll, select) says about the DIAG control framework: the item
that looked like type `0x00`, the `APPL RFC_TR` transport, how the grid's rows
travel, and the item order that opens a custom container with a control in it.

The capture holds a real system's identifiers — a destination string with a
host and a public address, session GUIDs, a user name. None of them are here.
Only structure is, and the synthetic fixture in `rfctr_test.go` is built by
hand.

Status: **confirmed** = read off the capture and consistent across every frame
of that shape; **inferred** = the plausible reading of what was seen;
**unknown** = bytes nobody has explained.

## The `0x00` item was a CHL mis-split — confirmed

`ParseItems` was leaving one raw item per screen-heavy `S->C` frame: a type
`0x00` of 20–28 KB, first seen around +13s and again at +30s, +33s, +36s. It
was never a real item type.

Every one of those followed an `CHL` item (type 0x09). `CHL` carries no length
field; its value length was assumed to be 3. It is 22. Read as 3, the parser
stopped three bytes into the `CHL` body, found a `0x00` there — `CHL` bodies
begin `00 09 00 00 00 00 45 …` — took it for a new item type `0x00`, and, not
knowing that type, dumped the whole rest of the frame as one raw item. The
28 KB was the tail of an ordinary frame, not a payload.

With `CHL` at 22 the same frames parse to the end: the bytes after the `CHL`
are `APPL VARINFO.02`, then the run of `SBA` / `SFE` / `SLC` / `APPL VARINFO.0b`
that paints the table's title line. No raw item is left on the capture except
genuinely unknown item types, of which there are none. The length 22 is also
pysap's. Fix and guard: `pkg/diag/items.go`, `TestParseItemsCHL`.

## `APPL RFC_TR` is a GUI-RFC call — inferred framing, confirmed contents

`APPL RFC_TR` (id 0x08) is the control framework's transport. The direction is
server to GUI: the server makes an RFC call *into* the SAP GUI, which acts as an
RFC server for its controls, and the call carries OLE-automation methods for
them. The sub-id is the leg of the call:

| Item | Seen carrying | Read |
|---|---|---|
| `RFC_TR.04` | destination, `GUICORE_BLOB_DIAG_PARSER`, `STREAM`, `MDS_CTRL_CONTROLLER`, all the menu texts | the request that sets a control up — its class, geometry, menus |
| `RFC_TR.00` | `IMPORT_XML`, `EXPORT_XML`, `XML_DATA_STREAM`, `<SVARS>…</SVARS>`, a large binary run | the data leg: rows in, rows out |
| `RFC_TR.01` | `EXPORT_XML`, `XML_DATA_STREAM`, a large binary run | the data leg, the other direction |
| `RFC_TR.06` | a session/context blob | inferred, not decoded |

These are ordinary `APPL` items: id, sid, a **two-byte** big-endian length, the
value. Confirmed — the largest `RFC_TR.04` is 29 692 bytes and splits cleanly
with the two-byte length; nothing here needs the longer length field an earlier
guess reached for. The `0x00` raw was the `CHL` bug, not `RFC_TR`.

The payload inside is **not** classic APPC. open-rfc-go's APPC decoder rejects
its first byte, `0x01`, as an "APPC protocol version 0x1"; it is the control
framework's own tag stream. What is stable across every `RFC_TR.04` — and all
`SplitRFCTR` in `pkg/diag/rfctr.go` claims — is:

- a fixed lead-in (60 bytes on the captured `.04`, header bytes
  `01 01 00 08 01 01 01 01 04 01 01 00 01 01 01 03 00 04 00 00 0e 0b …`),
- then **length-prefixed strings**: a two-byte big-endian length, then that
  many bytes, NUL-padded. The first string is the RFC destination the GUI
  calls back on. `GUICORE_BLOB_DIAG_PARSER`, `STREAM`, `IMPORT_XML`,
  `EXPORT_XML`, `XML_DATA_STREAM`, the menu texts all frame the same way.

`SplitRFCTR` returns the lead-in, the destination, and the strings, and is
best-effort: it walks strings by that framing and skips the tag and binary runs
between them. The tag bytes between strings (`01 27 00 07 …`, `00 0b 01 02 …`,
`03 37 02 01 …`) are **unknown** — enough regularity to step over, not enough to
name.

The method names `IMPORT_XML` and `EXPORT_XML` with a parameter named
`XML_DATA_STREAM` are the automation calls that move a control's data. Confirmed
as text; that they are method-and-parameter of the GUICORE blob parser is
inferred from the names and their company (`GUICORE_BLOB_DIAG_PARSER`,
`MDS_CTRL_CONTROLLER`).

## How the ALV rows travel — confirmed shape

Two layers carry the grid, and they cooperate.

**The DIAG XML item (`0x11`) runs the DataManager.** Its document is
`<?xml … encoding="sap*"?><DATAMANAGER>…</DATAMANAGER>` and it is the grid's
model protocol:

```
<DATAMANAGER>
 <TABLES>
  <DATACHANGES HANDLE="1">
   <IT IDX="0" OP="C"/>
   <IT IDX="1" C1="1" C2="4" OP="I"/>
   <IT IDX="2" C1="2" C2="23" OP="I"/>
   …
  </DATACHANGES>
  <DATAREQUEST HANDLE="3" NLINES="1000" FIRSTLINE="1"/>
 </TABLES>
 <CONTROLS>
  <CONTROL SHELLID="117">
   <PROPERTY VALUE="645" NAME="120"/>
   …
   <GENERIC FVCID="4" FVRID="39" FVRIDS="0"/>
  </CONTROL>
 </CONTROLS>
</DATAMANAGER>
```

Read off the capture:

- A grid's data is a set of numbered **tables** (`HANDLE`), each a logical
  column store. `DATACHANGES` lists cell edits by row (`IT IDX`) and column
  range (`C1`..`C2`), with `OP` `C` (clear) or `I` (insert). Confirmed.
- `DATAREQUEST HANDLE NLINES FIRSTLINE` is the server asking the GUI to send
  `NLINES` rows from `FIRSTLINE` — a scroll or a fetch. On this capture
  `NLINES="1000"`, `FIRSTLINE="1"`. Confirmed.
- `CONTROL SHELLID` addresses one control shell; `PROPERTY NAME/VALUE` sets its
  state (`CurrentCellCol`, `FirstVisibleRow`, `FirstVisibleRowID` and numeric
  names like `120`, `300`). Confirmed.
- `GENERIC FVCID FVRID FVRIDS` is the generic first-visible cell/row state
  (first-visible column id, row id, row-id-set). Inferred from the names and
  their agreement with the `FirstVisible*` properties beside them. This is the
  "GENERIC F…" seen in the XML items.

**The RFC_TR data legs carry the bytes.** The row contents themselves ride the
`RFC_TR.00` / `.01` `IMPORT_XML` / `EXPORT_XML` calls as an `XML_DATA_STREAM`.
On the capture the bulk of an `RFC_TR.00` (roughly 5 KB–21 KB of a 22 KB item)
is a binary run, not readable XML — a serialized data stream, inferred to be the
grid's rows in the GUICORE blob format the parser name advertises. Only its
envelope (`IMPORT_XML`, `EXPORT_XML`, `XML_DATA_STREAM`, a small `<SVARS>`
block) is readable; the payload itself is **unknown**.

So: the DIAG XML item says *which* rows changed and *how many* to send from
where; the RFC_TR data legs move the actual cell bytes. The grid never appears
as one plain table on the wire.

## Opening a custom container with a control — confirmed order

One `S->C` frame (3 171 bytes, +18.3s) both builds the screen and puts the grid
in it. The control-framework items arrive, after the screen's own items, in this
order:

1. `APPL CONTAINER.01` — allocate the container region. Value is a nine-byte
   geometry block.
2. `APPL CONTAINER.0a` — attach/flags for the region (nine bytes).
3. `APPL CONTAINER.06` — name the custom container:
   `<containerName>\0<program>\0<screen>\0`, e.g.
   `G_TABSTRIP\0SAPLWB_CUSTOMIZING\00999\0` or
   `TOOLAREA\0SAPLWB_CUSTOMIZING\00400\0`. This ties the region to the container
   the dynpro declares. Confirmed shape.
4. `APPL CONTROL.CONTROL_PROPERTIES` — instantiate the control shell in that
   region. The value is tag-framed: a shell id, then two-byte-length-prefixed
   properties — the shell class `GRID1`, then geometry (`1`, `0`, `0`, `55`,
   `238`). Confirmed as text; the exact tag numbering is inferred.
5. `XML` (`0x11`) — the DataManager handshake for the new shell (a bare
   `<DATAMANAGER/>` first, then the populated document above).

`CONTAINER.08` and `CONTAINER.09` also appear on setup frames, nine bytes each,
carrying region geometry (`… 00 4e 00 1e` = 78 × 30 and `… 00 4c 00 1b`); `08`
reads as a single region and `09` as a split, inferred from the pair of
sizes each holds. The `UI_EVENT.UI_EVENT_SOURCE` item registers which control
events the GUI should report back; its 16-byte value is **unknown** beyond that.

After this frame the `RFC_TR.04` setup call (menus, `MDS_CTRL_CONTROLLER`) and
the `RFC_TR.00`/`.01` data legs follow, and the grid is live.

## What is still unknown

- The tag bytes between strings in an `RFC_TR` payload.
- The `XML_DATA_STREAM` binary body — the grid rows in GUICORE blob form.
- The internals of `CONTROL_PROPERTIES` and `UI_EVENT_SOURCE` past the readable
  fields.
- `RFC_TR.06`'s session/context blob.
