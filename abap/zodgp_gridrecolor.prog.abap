*&---------------------------------------------------------------------*
*& Classic CL_GUI_ALV_GRID (docking container on the selection screen, no
*& SE51 dynpro). 'Recolor' toolbar button colours one more cell and calls
*& refresh_table_display( is_stable, i_soft_refresh='X' ) — the soft refresh.
*& Run through tap, click Recolor a few times, Back; compare wire traffic to
*& the SALV full re-send. (Source mirrors ADT ZODGP_GRIDRECOLOR.)
*&---------------------------------------------------------------------*
REPORT zodgp_gridrecolor.
