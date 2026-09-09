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
