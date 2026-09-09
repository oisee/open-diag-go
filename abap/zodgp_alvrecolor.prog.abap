*&---------------------------------------------------------------------*
*& An ALV grid with a 'Recolor' toolbar button. Each click colours ONE MORE
*& cell (click 1 -> 1 cell, click 2 -> 2, click 3 -> 3) and refreshes, so a
*& tap capture shows whether the ALV re-sends the whole grid or a small
*& per-cell delta. If it's a delta, we can force-push cell recolours to
*& animate colours cheaply (an LED display on ALV). Run through tap, click
*& Recolor a few times, then Back.
*&---------------------------------------------------------------------*
REPORT zodgp_alvrecolor.
" (source mirrors the version deployed to A4H; see ADT ZODGP_ALVRECOLOR)
