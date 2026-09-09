*&---------------------------------------------------------------------*
*& A classic ABAP list: WRITE output, no controls, so the DIAG stream
*& shows how the old list processor puts text on the screen — ground
*& truth for translating text lists. Run it through tap, then Back.
*&---------------------------------------------------------------------*
REPORT zodgp_list LINE-SIZE 120 LINE-COUNT 65.

START-OF-SELECTION.
  DATA lv_sq TYPE i.
  WRITE: / 'odgp classic list  --  WRITE output over DIAG'.
  ULINE.
  WRITE: / 'idx' COLOR COL_HEADING, 10 'label', 30 'value', 50 'square'.
  ULINE.
  DO 20 TIMES.
    lv_sq = sy-index * sy-index.
    WRITE: / sy-index COLOR COL_KEY,
           10 'row',
           30 sy-index,
           50 lv_sq.
  ENDDO.
  ULINE.
  WRITE: / 'end of list'.
