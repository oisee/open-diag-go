# Classic ABAP lists on the wire — how WRITE output is positioned and coloured

A classic list — the output of a report's `WRITE` statements, the plain
character screen with a header, `ULINE` separators and rows — does **not**
travel over DIAG as a `DYNT_ATOM` screen. It is painted as a stream of
positioned, single-attribute text runs. This was read off one captured report
list whose content was known in full — a header line, four `ULINE` separators
and twenty numbered rows, columns `idx / label / value / square`, row *N*
carrying value *N* and square *N\*N*, and the first column coloured
`COL_KEY` — so the positions, lengths and colours below could be lined up
against ground truth. Only the harmless list text is quoted; no captured bytes,
identifiers, host, user or session appear here.

## The run and its four items

The list body is a repeating group of four items, always in this order:

| item | type / id.sid | len | carries |
|------|---------------|-----|---------|
| `SBA`        | 0x0b (fixed) | 2 | the cursor position of the run |
| `SFE`        | 0x0a (fixed) | 3 | the display attribute (colour, text-vs-ruled) |
| `SLC`        | 0x13 (fixed) | 2 | the run's length, big-endian |
| `VARINFO.0b` | APPL 0x0c.0b | n | the run's characters, plain text, no prefix |

The three attribute items **precede** the `VARINFO.0b` they describe. A run
therefore binds to the most recent `SBA`, `SFE` and `SLC` seen before it. Each
run in the capture carried its own fresh trio; none was reused across runs.

A whole line of the list is several such runs, split wherever the colour or
attribute changes — so the header line `idx … label … value … square` is not
one run but one run per coloured cell plus the blank gaps between them.

### SBA — position — confirmed

Two bytes: **byte 0 is the row, byte 1 is the column, both 0-based.**
Confirmed against ground truth on every one of the ~30 lines: the header text
sat at row 0 col 0, the column header line at row 4, the twenty data rows at
rows 6 through 25, the trailing `ULINE` at row 26 and `end of list` at row 27,
each column starting at its known column (the value column at col 29, the
square column at col 49). Rows and columns never exceeded these small values,
so the high bits of each byte were only ever seen as part of the number, not as
flags.

### SLC — length — confirmed

Two bytes, big-endian, and it equals the byte count of the following
`VARINFO.0b` run on every single segment (e.g. `00 0b` = 11 ahead of an
eleven-character value cell, `00 78` = 120 ahead of a full-width `ULINE`).
It is a declared length the parser can check the text against, not something it
must have to read the text (the `VARINFO.0b` item carries its own length in the
APPL header).

### SFE — colour and attribute — colour confirmed, the rest inferred

Three bytes. **Byte 1 is the SAP list colour number**, the same numbering an
ABAP report gives `FORMAT COLOR`. Confirmed on ≥2 lines each:

| SFE byte 1 | colour | where, in the capture |
|-----------:|--------|------------------------|
| `0x00` | off / normal body | all body text and blank gaps |
| `0x01` | `COL_HEADING` | the `idx` column header cell |
| `0x04` | `COL_KEY` | the first (idx) column of every one of the 20 data rows |

The colour is **not** carried in a separate item and **not** in the top bits of
a length — it is this one byte of `SFE`. The other standard list colours
(`COL_NORMAL` 2, `COL_TOTAL` 3, `COL_POSITIVE` 5, `COL_NEGATIVE` 6,
`COL_GROUP` 7) did not occur in this capture and are inferred by their standard
numbering.

**Byte 0** distinguishes a text run from a ruled run: it was `0x0a` on every
text run and `0x08` on every `ULINE` run (the horizontal rule arrives as a run
of the character the SAP font renders as a line). **Byte 2** moved with it —
`0x00` on text, `0x08` on the `ULINE` runs. Both bytes changing together mark a
ruled/symbol run rather than text; the exact bit meaning is inferred (checked
on the four full-width `ULINE` lines and one inline `ULINE`, against all the
text runs). Colour (byte 1) is independent of these: a `ULINE` run was
`08 00 08`, plain body text `0a 00 00`, a key cell `0a 04 00`, a heading cell
`0a 01 00`.

## The frame around the runs

The list frame is otherwise an ordinary server frame: the mandatory
`ST_R3INFO` environment block, a `VARINFO.09` title, `MNUENTRY` menus, a
`DYNN.01`, and a **small `DYNT_ATOM`** — the list viewer's dynpro shell, not the
list content. The list text is entirely in the `SBA`/`SFE`/`SLC`/`VARINFO.0b`
stream. A reader wanting the list should therefore prefer the run stream when
`VARINFO.0b` items are present and treat the `DYNT_ATOM` as the shell.

Where two runs overprint the same cell — the report's nine-wide `idx` column
and the `label` column's `row` text share a column — the later run wins, the
same as the GUI shows it.

## What the parser does with this

`pkg/diag/list.go` turns a frame's items into `[]ListSegment` with
`ParseListItems`: it walks the items, remembers the last `SBA`/`SFE`/`SLC`, and
emits one `ListSegment{Row, Col, Length, Color, Attr, Text}` per `VARINFO.0b`,
consuming the trio so an orphan run (no fresh position) is flagged `Inferred`.
`GroupListLines` gathers segments into rows; `HasListSegments` tells a list
frame from a plain dynpro screen. `pkg/tui`'s `RenderList` paints the segments
onto the character grid at their positions.
