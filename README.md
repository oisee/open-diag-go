# open-diag-go (odgp)

SAP GUI as a display. A server that speaks DIAG — the protocol between SAP GUI
and the dispatcher — well enough for a real SAP GUI to connect to it and draw
one dynpro after another from our own description, at our own cadence. No SAP
system behind it: the frames come from a demo engine.

Pure Go, no SAP libraries, Apache-2.0. Built on
[`open-rfc-go`](https://github.com/oisee/open-rfc-go) for the NI transport and
the framing-aware proxy, and on `vibing-steampunk`'s `sapcompress` for the LZH
and LZC that DIAG uses on the wire.

> Research preview. Nothing here has drawn a frame yet. Phase 0 is the gate:
> see [docs/plan.md](docs/plan.md).

## The one thing everything hangs on

A dynpro is redrawn only when the GUI does a roundtrip, and a roundtrip happens
on user input. A server-driven animation needs the server to *ask* the GUI for
a roundtrip. A report on a live system does that today with an asynchronous
RFC timer — `STARTING NEW TASK … PERFORMING … ON END OF TASK` — and the GUI
obliges: something in the DIAG stream tells it to send a PAI. That item is the
*kick*. Phase 0 puts such a report on a sandbox, captures the traffic with
`tap`, and finds the kick with `lens`. Its exact encoding is what Phase 0
delivers; that it exists is inferred from the report working, not known.

Our own server never needs RFC: it sends the kick on its own timer.

## Tools

| | |
|---|---|
| `cmd/tap` | Proxy between a real SAP GUI and a sandbox's dispatcher port; writes every NI frame of both directions to JSONL with timestamps. Works without SNC only. |
| `cmd/lens` | Reads a tap capture: DIAG header, decompression, items; diffs neighbouring frames. Unknown items stay raw. |
| `pkg/diag` | The item catalog — each entry confirmed, inferred or raw — and, later, the serializer. |
| `pkg/frame` | The canvas: a grid of character cells with attributes, widget placements, title, status. |
| `pkg/runtime` | Effects and the timeline, ported from bytebeat-abap's `ZIF_O4D_EFFECT`. |
| `abap/` | The Phase 0 probe: a report that redraws itself on an aRFC timer. |
| `captures/` | Captures from the sandbox. |

```bash
go build ./cmd/tap ./cmd/lens
./tap -listen :3200 -target sandbox.example:3200 -dump captures/probe.jsonl   # then point SAP GUI at localhost:3200
./lens captures/probe.jsonl                                                  # what went by, item by item
```
