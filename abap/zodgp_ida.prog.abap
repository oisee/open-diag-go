*&---------------------------------------------------------------------*
*& ALV with Integrated Data Access (CL_SALV_GUI_TABLE_IDA): a classic ALV
*& window whose data is fetched through SADL straight into HANA (the SELECT
*& and aggregation are pushed down), the renderer recommended before Fiori.
*& Run through tap and watch how the grid asks for data and how rows come
*& back — the SADL data channel over DIAG. Sort/filter/scroll to see the
*& push-down requests.
*&---------------------------------------------------------------------*
REPORT zodgp_ida.

START-OF-SELECTION.
  TRY.
      cl_salv_gui_table_ida=>create(
        iv_table_name = 'T100'
      )->fullscreen( )->display( ).
    CATCH cx_salv_ida_contract_violation INTO DATA(lx_ida).
      MESSAGE lx_ida->get_text( ) TYPE 'E'.
  ENDTRY.
