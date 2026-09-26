/* race_condition_fixed.pml
 * Misma lógica, pero la sección crítica (leer-sumar-escribir)
 * está protegida por un mutex. Equivale a sync.Mutex en Go.
 */
int  global_sum_kwh = 0;
int  expected_sum   = 0;
byte finished       = 0;
bool mutex          = false;   /* false = libre, true = ocupado */

proctype Worker(int partial_kwh) {
    int local_sum;
    atomic { !mutex -> mutex = true };       /* Lock(): esperar y tomar  */
    local_sum = global_sum_kwh;              /* --- sección crítica ---  */
    local_sum = local_sum + partial_kwh;
    global_sum_kwh = local_sum;              /* ------------------------ */
    mutex = false;                           /* Unlock()                 */
    finished++
}

init {
    expected_sum = 125430 + 98650 + 143210;  /* = 367290 kWh */
    run Worker(125430);
    run Worker(98650);
    run Worker(143210);
    (finished == 3);
    assert(global_sum_kwh == expected_sum)
}
