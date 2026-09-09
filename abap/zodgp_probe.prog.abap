*&---------------------------------------------------------------------*
*& Phase 0 probe: a screen that redraws itself on an aRFC timer.
*&---------------------------------------------------------------------*
*& Start it, leave the selection screen alone, and watch the counter
*& move. Every tick is one roundtrip the GUI made without a key being
*& pressed: whatever the server sent to make it do that is the kick,
*& and tap/lens are there to catch it. Stop with the tick box.
*&---------------------------------------------------------------------*
REPORT zodgp_probe.

PARAMETERS: p_ms    TYPE i DEFAULT 300,
            p_run   TYPE abap_bool AS CHECKBOX DEFAULT 'X',
            p_ticks TYPE i MODIF ID dsp,
            p_time  TYPE c LENGTH 12 MODIF ID dsp,
            p_bar   TYPE c LENGTH 40 MODIF ID dsp.

DATA gv_armed TYPE abap_bool.

AT SELECTION-SCREEN OUTPUT.
  LOOP AT SCREEN.
    IF screen-group1 = 'DSP'.
      screen-input = 0.
      MODIFY SCREEN.
    ENDIF.
  ENDLOOP.
  IF p_run = abap_true AND gv_armed = abap_false.
    PERFORM arm.
  ENDIF.

AT SELECTION-SCREEN.
  " A PAI: the user's, or the one the timer made the GUI send.
  IF sy-ucomm = 'ONLI' OR sy-ucomm = 'CRET'.
    LEAVE PROGRAM.
  ENDIF.
  IF p_run <> abap_true.
    gv_armed = abap_false.
  ENDIF.

FORM arm.
  CALL FUNCTION 'ZODGP_TIMER'
    STARTING NEW TASK 'ODGP'
    PERFORMING on_tick ON END OF TASK
    EXPORTING
      iv_ms = p_ms.
  gv_armed = abap_true.
ENDFORM.

FORM on_tick USING iv_task TYPE clike.
  RECEIVE RESULTS FROM FUNCTION 'ZODGP_TIMER'.
  p_ticks = p_ticks + 1.
  p_time = sy-uzeit.
  DATA(lv_pos) = p_ticks MOD 40.
  CLEAR p_bar.
  p_bar+lv_pos(1) = '#'.
  gv_armed = abap_false.
ENDFORM.
