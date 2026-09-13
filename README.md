# open-diag-go (odg)

SAP GUI as a display. A server that speaks DIAG — the protocol between SAP GUI
and the dispatcher — well enough for a real SAP GUI to connect to it and draw
one dynpro after another from our own description, at our own cadence. No SAP
system behind it: the frames come from a demo engine.

Pure Go, no SAP libraries, MIT-licensed. Built on
[`open-rfc-go`](https://github.com/oisee/open-rfc-go) for the NI transport and
the framing-aware proxy, and on `vibing-steampunk`'s `sapcompress` for the LZH
and LZC that DIAG uses on the wire.

> **Try it live:** point a SAP GUI at `demo.desude.su` (Instance Number **00**,
> no SNC), or watch in your terminal with
> [**sap-tui**](https://github.com/oisee/sap-tui): `sap-tui demo.desude.su:3200`.
> The light-show it draws is [**sap-lsd**](https://github.com/oisee/sap-lsd),
> built on this library.

📺 **See it in action** ([watch on YouTube](https://www.youtube.com/watch?v=Pszxxj-OUAk)):

[![open-diag-go — a demoscene light-show drawn over SAP DIAG](https://img.youtube.com/vi/Pszxxj-OUAk/maxresdefault.jpg)](https://www.youtube.com/watch?v=Pszxxj-OUAk)

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

## License & disclaimer

MIT — see [LICENSE](LICENSE).

This is an independent interoperability / research project. It is **not
affiliated with, authorized, or endorsed by SAP SE**. "SAP" and related marks
are trademarks of SAP SE, used here only to name the protocol this speaks.
Point it at systems you are allowed to test; the public light-show at
`demo.desude.su:3200` is there to play with.
