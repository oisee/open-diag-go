*&---------------------------------------------------------------------*
*& An icon probe: WRITE a handful of icons three ways so the DIAG stream
*& shows how the list processor carries an icon, versus a raw token, versus
*& plain text. The @xx@ token drawn in a plain dynpro label does NOT become
*& a picture (we saw it print literally), so the truth is here, in the list
*& channel. Run through tap, then Back, then decode with:
*&
*&   lens -only S->C -values captures/<file>.jsonl | less
*&
*& and compare the SBA/SFE/SLC/VARINFO runs of the AS ICON lines against the
*& raw and the plain ones.
*&---------------------------------------------------------------------*
REPORT zodgp_icon LINE-SIZE 120 LINE-COUNT 65.
INCLUDE <icon>.

START-OF-SELECTION.
  WRITE: / 'odgp icon probe  --  how DIAG carries an icon'.
  ULINE.

  " 1) The canonical path: WRITE ... AS ICON. This is what we want to see
  "    on the wire — the format the GUI turns into a bitmap.
  WRITE: / 'AS ICON' COLOR COL_HEADING.
  WRITE: / icon_green_light  AS ICON, 20 'icon_green_light'.
  WRITE: / icon_yellow_light AS ICON, 20 'icon_yellow_light'.
  WRITE: / icon_red_light    AS ICON, 20 'icon_red_light'.
  WRITE: / icon_led_green    AS ICON, 20 'icon_led_green'.
  WRITE: / icon_checked      AS ICON, 20 'icon_checked'.
  WRITE: / icon_okay         AS ICON, 20 'icon_okay'.
  WRITE: / icon_cancel       AS ICON, 20 'icon_cancel'.
  ULINE.

  " 2) The same constant WITHOUT 'AS ICON'. If the list processor still
  "    substitutes, the token alone is enough; if not, AS ICON is the switch.
  WRITE: / 'raw constant, no AS ICON' COLOR COL_HEADING.
  WRITE: / icon_green_light, 20 'icon_green_light raw'.
  WRITE: / icon_red_light,   20 'icon_red_light raw'.
  ULINE.

  " 3) A hand-written token and plain text, as controls.
  WRITE: / 'literal text' COLOR COL_HEADING.
  WRITE: / '@0A@',         20 'typed @0A@'.
  WRITE: / 'plain'         COLOR COL_NORMAL, 20 'no icon anywhere'.
  ULINE.
  WRITE: / 'end of list'.
