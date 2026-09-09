*&---------------------------------------------------------------------*
*& A coloured ALV grid: every cell gets a colour from a moving pattern, so a
*& tap capture shows WHERE the per-cell colour rides in the ALV data blob.
*& This is the capture we need before we can encode a coloured ALV of our own
*& (an "LED display" on ALV). Run through tap; scroll/sort a bit so rows page
*& in, then Back. Decode with pkg/alv.
*&---------------------------------------------------------------------*
REPORT zodgp_alvcolor.

TYPES: BEGIN OF ty_row,
         r  TYPE i,
         v1 TYPE c LENGTH 6,
         v2 TYPE c LENGTH 6,
         v3 TYPE c LENGTH 6,
         v4 TYPE c LENGTH 6,
         t_color TYPE lvc_t_scol,
       END OF ty_row.

DATA: gt      TYPE STANDARD TABLE OF ty_row,
      gs      TYPE ty_row,
      ls_col  TYPE lvc_s_scol.

START-OF-SELECTION.
  DO 12 TIMES.
    CLEAR gs.
    gs-r  = sy-index.
    gs-v1 = 'AAAAAA'.
    gs-v2 = 'BBBBBB'.
    gs-v3 = 'CCCCCC'.
    gs-v4 = 'DDDDDD'.
    DO 4 TIMES.
      CLEAR ls_col.
      ls_col-fname     = |V{ sy-index }|.
      ls_col-color-col = ( gs-r + sy-index ) MOD 7 + 1.
      ls_col-color-int = 1.
      ls_col-color-inv = 0.
      APPEND ls_col TO gs-t_color.
    ENDDO.
    APPEND gs TO gt.
  ENDDO.

  TRY.
      cl_salv_table=>factory(
        IMPORTING r_salv_table = DATA(lo_alv)
        CHANGING  t_table      = gt ).
      lo_alv->get_columns( )->set_color_column( 'T_COLOR' ).
      lo_alv->get_columns( )->set_optimize( ).
      lo_alv->display( ).
    CATCH cx_salv_msg INTO DATA(lx).
      MESSAGE lx->get_text( ) TYPE 'E'.
  ENDTRY.
