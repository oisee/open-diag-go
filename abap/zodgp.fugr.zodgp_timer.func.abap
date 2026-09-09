FUNCTION zodgp_timer
  IMPORTING VALUE(iv_ms) TYPE i DEFAULT 300.
  " The timer behind the probe: started as an asynchronous RFC, it does
  " nothing but wait, and its completion is what makes the GUI do a
  " roundtrip. Remote-enabled, because STARTING NEW TASK needs that.
  DATA lv_seconds TYPE p LENGTH 8 DECIMALS 3.
  lv_seconds = iv_ms / 1000.
  IF lv_seconds < '0.050'.
    lv_seconds = '0.050'.
  ENDIF.
  WAIT UP TO lv_seconds SECONDS.
ENDFUNCTION.
