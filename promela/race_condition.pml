/* race_condition.pml
 * Modelo SIN protección: tres workers suman su aporte parcial (kWh)
 * a un acumulador global compartido. Se espera que Spin encuentre
 * una intercalación con pérdida de actualización (lost update).
 */
int  global_sum_kwh = 0;   /* acumulador global compartido          */
int  expected_sum   = 0;   /* suma correcta, calculada en init      */
byte finished       = 0;   /* contador de workers que terminaron    */

proctype Worker(int partial_kwh) {
    int local_sum;
    local_sum = global_sum_kwh;              /* 1. leer el acumulador   */
    local_sum = local_sum + partial_kwh;     /* 2. sumar el aporte      */
    global_sum_kwh = local_sum;              /* 3. escribir el resultado */
    finished++
}

init {
    expected_sum = 125430 + 98650 + 143210;  /* = 367290 kWh */
    run Worker(125430);
    run Worker(98650);
    run Worker(143210);
    (finished == 3);                         /* esperar a los 3 workers */
    assert(global_sum_kwh == expected_sum)
}
