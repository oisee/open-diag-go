The Phase 0 probe, as deployed to the sandbox in `$TMP`:

- `ZODGP` — function group; `ZODGP_TIMER` — remote-enabled module that waits
  `iv_ms` milliseconds. Created with vsp: `object_type FUGR/FF, rfc_enabled: true`.
- `ZODGP_PROBE` — the report. Run it, leave the selection screen alone: the
  counter, the time and the bar move on their own, one roundtrip per tick.

Capture recipe: `tap -listen :3200 -target <sandbox>:3200 -dump captures/probe.jsonl`,
SAP GUI pointed at the machine running tap (system number 00), logon, SE38,
ZODGP_PROBE, F8, twenty ticks, then the tick box off and back. `lens
captures/probe.jsonl -only S->C` shows the frames the server sent between
keystrokes.

Screen 0100 is not made in the screen painter: `abap/screen_0100.json` is
handed to `RPY_DYNPRO_INSERT` through ZADT_VSP's function bridge
(`vsp -s <sandbox> debug`, then `call RPY_DYNPRO_INSERT HEADER={…}
CONTAINERS=[…] FIELDS_TO_CONTAINERS=[…] FLOW_LOGIC=[…] SUPPRESS_CORR_CHECKS=X
SUPPRESS_EXIST_CHECKS=X SUPPRESS_GENERATE=X SUPPRESS_EXTENDED_CHECKS=X`, the
JSON compact). Two things the function does not say: fields belong to a
container, and the `SCREEN` container has to be declared in `CONTAINERS` or
every field is dropped without a word; and a field's `TEXT` is its template
(`_________V` for a right-justified INT4), the vocabulary being `TEXT`,
`TEMPLATE`, `CHECK`, `OKCODE` in `TYPE`, read off a generated selection
screen with `RPY_DYNPRO_READ`.
