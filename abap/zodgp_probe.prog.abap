*&---------------------------------------------------------------------*
*& Phase 0 probe: a dynpro that redraws itself on an aRFC timer.
*&---------------------------------------------------------------------*
*& The selection screen takes the interval; screen 0100 shows one
*& number. PBO arms the timer, the timer's end makes the GUI send a PAI
*& without a key being pressed — that roundtrip is what the capture is
*& after — PAI counts it and the next PBO arms again. Back, Exit or
*& Cancel leave. Screen 0100: abap/screen_0100.json.
*&---------------------------------------------------------------------*
REPORT zodgp_probe.

PARAMETERS: p_ms  TYPE i DEFAULT 300,
            p_run TYPE abap_bool AS CHECKBOX DEFAULT abap_true.

DATA: gv_ticks TYPE i,
      gv_subrc TYPE i,
      gv_armed TYPE abap_bool,
      ok_code  TYPE sy-ucomm.

START-OF-SELECTION.
  CALL SCREEN 100.

MODULE status_0100 OUTPUT.
  SET PF-STATUS space.
  SET TITLEBAR space.
  IF p_run = abap_true AND gv_armed = abap_false.
    PERFORM arm.
  ENDIF.
ENDMODULE.

MODULE user_command_0100 INPUT.
  CASE ok_code.
    WHEN 'BACK' OR 'EXIT' OR 'CANC'.
      LEAVE PROGRAM.
    WHEN 'TICK'.
      " The timer's PAI: nothing to do, PBO redraws and arms again.
  ENDCASE.
  CLEAR ok_code.
ENDMODULE.

FORM arm.
  " ZTETRIS's way: an asynchronous RFC that only waits, and a callback
  " that queues a function code. The queued code is what makes the GUI
  " roundtrip once the callback has run; without SET USER-COMMAND the
  " count moved in memory and the screen never heard of it.
  CALL FUNCTION 'ZODGP_TIMER'
    STARTING NEW TASK 'ODGP'
    PERFORMING on_tick ON END OF TASK
    EXPORTING
      iv_ms = p_ms
    EXCEPTIONS
      system_failure        = 1
      communication_failure = 2
      resource_failure      = 3
      OTHERS                = 4.
  IF sy-subrc = 0.
    gv_armed = abap_true.
  ELSE.
    gv_subrc = sy-subrc.
  ENDIF.
ENDFORM.

FORM on_tick USING iv_task TYPE clike.
  RECEIVE RESULTS FROM FUNCTION 'ZODGP_TIMER'.
  gv_ticks = gv_ticks + 1.
  gv_armed = abap_false.
  SET USER-COMMAND 'TICK'.
ENDFORM.
