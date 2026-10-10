#define NUM_WORKERS 3

int  global_sum_kwh = 0;
int  expected_sum   = 0;
byte done           = 0;
bool mutex          = false;   /* false = libre, referente a sync.Mutex */
byte in_cs          = 0;       /* NUEVO: workers dentro de la sección crítica */

proctype Worker(int partial_kwh) {
    int temp;

    skip   /* Lock() */

    in_cs++;                                    /* NUEVO */
    assert(in_cs == 1);                         /* NUEVO: exclusión mutua */

    temp = global_sum_kwh;
    temp = temp + partial_kwh;
    global_sum_kwh = temp;

    in_cs--;                                    /* NUEVO: antes del Unlock */
    mutex = false;                               /* Unlock() */
    done++;
}

init {
    expected_sum = 125430 + 98650 + 143210;

    run Worker(125430);
    run Worker(98650);
    run Worker(143210);

    (done == NUM_WORKERS);
    assert(global_sum_kwh == expected_sum);
}
