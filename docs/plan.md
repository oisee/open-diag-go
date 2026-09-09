# Plan

Phases, with a gate on Phase 0.

**Phase 0 — the probe and the kick. GO / NO-GO.** On the sandbox, a report in
the spirit of ZTETRIS: a screen that redraws itself on an aRFC timer. Capture
the traffic, find and decode the item with which the server makes the GUI send
a PAI. Output: the kick's exact signature. If it is not in the stream — if the
mechanism sits inside the Control Framework, say — stop and rethink the genre.

**Phase 1 — capture and decode toolchain.** The item catalog for the subset
the demo uses: static dynpro elements (fields, buttons, checkboxes, frames,
icons, title, status) and the kick.

**Phase 2 — translation, a still frame.** The frame model and a serializer to
DIAG. Done when a real SAP GUI connects to our server and draws one still
screen from our description.

**Phase 3 — the timer and the first moving frame.** Timer → kick → PAI arrives
→ next frame. One effect (starfield), proof of animation.

**Phase 4 — the effect engine and the timeline.** bytebeat-abap's `ZCL_O4D_*`
on the frame model; the `ZCL_O4D_DEMO` orchestrator as it is, with a widget
grid for a backend instead of a vector.

## Tools

1. **tap** — the interceptor. Proxy between a real GUI and the sandbox, port
   32xx in, raw NI frames of both directions with timestamps out. Built on
   `open-rfc-go/sniffer`. Only without SNC; manageable on a sandbox, not for
   production systems.
2. **lens** — decoder and annotator. Tap frames in; annotated items and the
   diff between neighbouring frames out. The diff is the point: animation is
   difference. Unknown items fall into raw.
3. **probe** — the ABAP report with the aRFC timer (200–500 ms cadence) and the
   capture recipe. One-off, Phase 0.
4. **catalog** — widget type → DIAG encoding, versioned, each entry confirmed /
   inferred / raw. The one source of truth for the serializer.
5. **frame** — the canvas. Not a DIAG structure: a grid of character cells
   with attributes, placements (type, row, col, length, text, list colour,
   intensified / inverse, icon code `@xx@`), title, status (text + S/W/E). No
   table controls, no ALV.
6. **runtime** — effects: `render(t)` writes into a frame; an orchestrator on a
   timeline (bars / BPM).
7. **serializer** — frame + catalog → the items of one screen. A diff mode —
   only changed cells — if the protocol allows it; whether a partial refresh is
   cheaper than a full one is a Phase 1 question.
8. **server** — the loop: handshake, one held dynpro, timer → kick → PAI →
   serializer(next frame). RFC-free.

## Decisions

- Language: Go from the start. The transport, the proxy and the compression
  exist in Go under Apache-2.0; a Python prototype on pysap would be GPL and
  a port of it a translation.
- Kick cadence: ours to choose; bounded by how fast the GUI redraws. Phase 3.
