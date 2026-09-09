*&---------------------------------------------------------------------*
*& Emits a status MESSAGE of a chosen type on each Enter, so the DIAG
*& stream shows how a Success/Warning/Error message (and its sound) is
*& sent — the item behind Success-Msg.wav / Warning-Msg.wav. Run through
*& tap: set P_TYPE, press Enter, hear the sound and catch the frame.
*&---------------------------------------------------------------------*
REPORT zodgp_msg.

PARAMETERS p_type TYPE c LENGTH 1 DEFAULT 'W'.

AT SELECTION-SCREEN.
  CASE p_type.
    WHEN 'S'.
      MESSAGE 'odgp success sound' TYPE 'S'.
    WHEN 'W'.
      MESSAGE 'odgp warning sound' TYPE 'W'.
    WHEN 'I'.
      MESSAGE 'odgp information' TYPE 'I'.
    WHEN OTHERS.
      MESSAGE 'odgp error sound' TYPE 'E'.
  ENDCASE.
