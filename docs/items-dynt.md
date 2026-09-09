# DYNT_ATOM — the screen on the wire

`APPL4 DYNT.DYNT_ATOM` (item type 0x12, id 0x09, sid 0x02) carries a
dynpro as a chain of *atoms*. Every visible element is one atom; a name
atom follows it giving the ABAP field name, and an xmlprop atom may follow
that with a `<Propertybag>` (tooltip, typeahead). The layout below was read
off the two Phase 0/1 captures: a logon screen, SE38, the screen painter,
the probe's selection screen, its screen 0100 pushed 67 times with only the
counter changing, and SE16 with an ALV grid. In numbers: 146 items,
1465 atoms, nine etypes. Code: `pkg/diag/dynt.go`; `lens` prints the atoms
under each item.

Status: **confirmed** = checked on at least two screens and consistent
across all atoms of that shape; **inferred** = the plausible reading of
what was seen; **unknown** = bytes nobody has explained.

Ground truth used: screen 0100 has a label "Ticks" at line 2 column 2, an
output-only INT4 field GV_TICKS at line 2 column 10, length 10, and the
OK-code field. Its item is 96 bytes and holds four atoms — label, name,
output field, name. The OK-code field does not appear in DYNT_ATOM.

## The atom header (12 bytes, every etype)

| Offset | Size | Field | Status | Evidence |
|---|---|---|---|---|
| 0 | 2 | Length, big-endian, **including these two bytes** | confirmed | Walking atoms by this length fills all 146 items exactly, 0 leftovers |
| 2 | 2 | Flag bytes | unknown | Kept raw; associations below |
| 4 | 1 | etype | confirmed | Nine values, each tied to what the element did on screen (table below) |
| 5 | 1 | Area: 1 inside a step loop, else 0 | inferred | Only the logon screen's info-text loop sets it (7 lines, one per row) |
| 6 | 1 | Line within the loop, 1-based; 1 elsewhere | inferred | Counts 1..7 through that loop; 1 on every other atom |
| 7 | 1 | Group: 1 on radio buttons and their name/xmlprop, else 0 | inferred | SE38 subobject radios, SE16 output-format radios |
| 8 | 2 | Row, 0-based | confirmed | "Ticks" row 1 = line 2; SE38 "Program" row 2 = line 3 |
| 10 | 2 | Column, 0-based | confirmed | "Ticks" col 1 = column 2; GV_TICKS col 9 = column 10 |

The two-byte width of row and column is inferred: rows reached 19 and
columns 78, so the high byte has only ever been zero. The three bytes at
5–7 are pysap's `area`, `block`, `group`; the loop reading of the first two
comes from one screen only.

## etypes

| etype | Name here | pysap name | What it was on screen | Status |
|---|---|---|---|---|
| 0x72 | name | FNAME_1 | ABAP name of the preceding element (`GV_TICKS`, `RS38M-PROGRAMM`, `%_P_MS_%_APP_%-TEXT`) | confirmed |
| 0x73 | button | PUSHBUTTON_2 | Pushbutton with caption and function code | confirmed |
| 0x78 | xmlprop | XMLPROP | `<Propertybag>…</Propertybag>` for the preceding element | confirmed |
| 0x7f | frame | FRAME_1 | Box with a title ("Subobjects", "Settings") | confirmed |
| 0x80 | checkbox | CHECKBUTTON_1 | `PARAMETERS … AS CHECKBOX`, screen-painter attribute boxes | confirmed |
| 0x81 | radio | RADIOBUTTON_1 | SE38 subobjects, SE16 output format | confirmed |
| 0x82 | input | EFIELD_2 | Input field (program name, P_MS, the logon fields) | confirmed |
| 0x83 | output | OFIELD_2 | Output field (GV_TICKS, selection-screen parameter texts, loop lines) | confirmed |
| 0x84 | label | KEYWORD_2 | Text label ("Ticks", "Program", "Table Name") | confirmed |

A name atom and an xmlprop atom carry the same row, column and attribute
byte as the element they describe (616 of 616 name atoms). In a step loop
the name atom was sent for the first line only.

## After the header, by etype

### input / output / label (0x82, 0x83, 0x84)

| Offset | Size | Field | Status | Evidence |
|---|---|---|---|---|
| 12 | 1 | Attribute bits (below) | confirmed | |
| 13 | 2 | Always 0x0000 | unknown | Zero on all 500 field atoms |
| 15 | 1 | Text length | confirmed | Equals the remaining bytes on all 500 atoms; 5 for "Ticks", 10 for GV_TICKS |
| 16 | 1 | Visible length | inferred | 30 where the program-name field shows 30 of 40; 12 on the user field whose typed value was 6 |
| 17 | 2 | Maximum characters | inferred | 40 on that program-name field; 60 on the command field |
| 19 | n | Text, padded with blanks to the text length | confirmed | Right-justified fields pad on the left: `"        0 "` |

The fixture in `dynt_test.go`: `0018 0000 84 000100 0001 0001 21 0000 05 05 0005 "Ticks"`.

### name / xmlprop (0x72, 0x78)

| Offset | Size | Field | Status |
|---|---|---|---|
| 12 | 1 | Attribute byte, repeating the element's | confirmed |
| 13 | n | Name or XML to the end of the atom; no terminator | confirmed |

### button (0x73)

| Offset | Size | Field | Status | Evidence |
|---|---|---|---|---|
| 12 | 1 | Attribute byte | confirmed | 0x20 on all |
| 13 | 1 | Width in characters | inferred | 16 for "Create", 2 and 3 for icon-only buttons |
| 14 | 1 | Height in rows | inferred | Always 1 |
| 15 | 2 | Offset of the function code, from the atom's first byte | confirmed | 43 of 43 |
| 17 | 2 | Offset of the caption, from the atom's first byte | confirmed | Always 19, i.e. right after this field |
| 19 | – | Caption, NUL-terminated (`@0Y\QCreate@ Create`) | confirmed | |
| – | – | Function code, NUL-terminated, with a leading `=` (`=NEW`) | confirmed | |

### frame (0x7f)

| Offset | Size | Field | Status | Evidence |
|---|---|---|---|---|
| 12 | 1 | Attribute byte | confirmed | 0x21 |
| 13 | 2 | Height in rows | inferred | 7 for a box at line 5 whose radios sat on lines 6–10 and whose buttons on line 12 sat outside |
| 15 | 2 | Width in characters | confirmed | Equals the remaining bytes on 22 of 22 frames |
| 17 | n | Title, padded to the width | confirmed | |

### checkbox / radio (0x80, 0x81)

| Offset | Size | Field | Status | Evidence |
|---|---|---|---|---|
| 12 | 1 | Attribute byte | confirmed | |
| 13 | 1 | State, `'X'` or `' '` | confirmed | The selected SE38 subobject, `P_RUN` set, the client toggling SE16's output format |
| 14 | 2 | Text length | confirmed | 51 server atoms |
| 16 | 6 | Unknown | unknown | Zero except on five screen-painter checkboxes: `00 00 03 00 03 00`, with the text starting `=HD` |
| 22 | 1 | Text length again | inferred | Equals offset 14 on every server atom |
| 23 | n | Text, padded | confirmed | |

When the client sends a radio group back it writes attribute, state, the
element's length (71, the length of those radio texts), six zero bytes, a
zero count and **one more zero byte**, with no text. The extra byte is not
explained.

## The attribute byte

| Bit | Name (pysap) | Seen on | Status |
|---|---|---|---|
| 0x01 | PROTECTED | Every label, GV_TICKS, selection-screen texts, frames | confirmed |
| 0x02 | INVISIBLE | The password field | inferred (one element) |
| 0x04 | INTENSIFY | Not seen set | unknown |
| 0x08 | JUSTRIGHT | GV_TICKS (INT4), P_MS, P_TICKS, MAX_SEL — every numeric field | confirmed |
| 0x10 | MATCHCODE | SE38's program-name field, which has F4 help | inferred (one element) |
| 0x20 | PROPFONT | Labels, selection-screen texts, the command field | confirmed |
| 0x40 | YES3D | Every input field | confirmed |
| 0x80 | COMBOSTYLE | SE38's program-name field, SE16's table-name, select-option and LIST_BRE fields, the client's screen-number entry | unknown |

## The two flag bytes (header offsets 2–3)

Kept raw. Associations, none stronger than that:

- byte 2 = 0x20 on numeric parameters (P_MS, P_TICKS, DYNNUMB, LIST_BRE,
  MAX_SEL); 0x04 on fields with a value help and on the loop lines.
- byte 3 bit 0x01 on the fields a client sent back changed; bit 0x02 on the
  text elements of selection screens and on SE38's "Program" label, but
  not on "Ticks"; bit 0x10 across SE16's select-option rows; bit 0x80 on
  the selected radio button of SE38 only.

## What a client sends

A client frame's DYNT_ATOM lists only what changed: the fields it typed
into (etype 0x82 with the typed text, the text length being what was
typed, the visible length and maximum unchanged) and the radio group it
switched. The logon frame carries the user and password fields this way in
clear; do not put a client frame into a fixture.

## Unknown

- The two flag bytes, beyond the associations above.
- Header bytes 5–7 outside a step loop and a radio group.
- The two zero bytes after a field's attribute byte, and the six bytes
  after a checkbox's text length (and the `=HD` prefix that comes with the
  non-zero variant).
- The trailing zero byte of a client's radio atom.
- Attribute bits 0x04 and 0x80.
- Whether row and column are really two bytes wide.
- The codepage of the text: the captures were single-byte; a Unicode GUI
  session may differ. `ST_R3INFO.CODEPAGE` should say.
- Every etype not listed: list boxes, tabstrips, subscreens, table
  controls, icons, the OK-code field. The 0100 frame carries only the two
  visible elements; where the command field lives is Phase 2's question.
