# Phase 0 — the probe, and what the wire showed

Sandbox: an S/4HANA 758 trial. Probe: `ZODGP_PROBE`, screen 0100, an aRFC
timer of 300 ms whose callback runs `SET USER-COMMAND 'TICK'` — the ZTETRIS
way (`KKPA_RFC_PING_AND_WAIT` … `ON END OF TASK`, then `SET USER-COMMAND`).
Captured with `tap`, read with `lens`. Verdict: **GO**, and the plan's one
unknown turned out not to exist.

## There is no kick item

Between the F8 that opened screen 0100 and the Back that left it, 20 seconds
apart, the client sent nothing. The server sent 67 frames on its own, one
every 305 ms (min 302, max 308), each a complete screen: the label, the
counter's new value, the OK-code field. No roundtrip was asked for and none
happened; the GUI redrew from what arrived.

So a server that wants to animate a dynpro does not need to make the GUI
send a PAI. It sends the next screen. What the aRFC trick does on a real
system is get the ABAP program past its own wait; the protocol underneath
was never the obstacle.

## Signatures

Confirmed on this capture (marked so in `pkg/diag` once the code follows):

- A pushed frame's DIAG header: `mode=00 com=00 stat=f0 err=00 type=00
  info=01 rc=00 compress=1`. The response to a client frame carries
  `stat=00`. The client's next frame after a run of pushed ones echoes
  `stat=f0`. So `mode_stat 0xF0` is the mark of a server-initiated screen
  and the client's acknowledgement of having seen one.
- A pushed frame's items are the same set a response carries: the
  `ST_R3INFO` block, `ST_USER.26` (a counter, +1 per frame), `DYNN.01`,
  `VARINFO.06`, `CONTAINER.01`, one `APPL4 DYNT.DYNT_ATOM` with the whole
  screen, `DYNT.0b`, and an `XML` item (the DATAMANAGER document).
- The screen itself is one `DYNT_ATOM` item of 96 bytes for a label, an
  output field and the OK-code field. Its atoms begin `0018 0000 84 00 01
  00 00 01 00 01 21 00 00 05 05 00 05 "Ticks"`: a 2-byte length, then
  attributes, then the text. Decoding the atom layout is Phase 1's first
  job; the field's value ("13", "14", …) sits inside it as text.
- Compression is LZC (`compress=1`) on everything but small responses.
- Every frame decoded with `sapcompress`; no unknown item type in 466
  frames.
- `NI_PING` / `NI_PONG` (8 bytes, ASCII) go by as bare NI payloads without
  a DIAG header; lens names them now instead of misreading them as one.

## What this changes in the plan

Phase 3's loop is not timer → kick → PAI → next frame. It is timer → next
frame. Phases 1 and 2 stand: the item catalog (the `DYNT_ATOM` layout
above all) and a serializer that produces one still screen a real GUI
draws. The server keeps the connection after the logon exchange and
pushes; whether it must answer the occasional client frame (the `stat=f0`
echo, `NI_PING`) is a Phase 3 detail.

Captures are not in git. The one this comes from is
`captures/probe.jsonl` on the machine that took it.
