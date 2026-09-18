# Caso de uso y selección del dataset

## Caso de uso

El caso de uso es la predicción del consumo eléctrico diario de hogares urbanos mediante regresión lineal. A partir de mediciones históricas de medidores inteligentes se construirán variables como consumos rezagados, tipo de tarifa y componentes de calendario. La variable objetivo es el consumo del siguiente periodo expresado en kWh.

Una predicción oportuna puede apoyar la planificación de demanda, la identificación de patrones de consumo y el uso más eficiente de la infraestructura energética. Esta relación sustenta la vinculación del proyecto con el ODS 11: Ciudades y comunidades sostenibles.

## Problema computacional

La fuente contiene lecturas cada media hora para miles de hogares. La auditoría encontró 167,932,474 filas, cantidad que excede la memoria práctica de una sesión convencional si todos los CSV se cargan al mismo tiempo con estructuras de alto nivel. El volumen permite estudiar el costo de la lectura, transformación y entrenamiento, y evaluar posteriormente si una implementación concurrente reduce el tiempo sin alterar el resultado numérico.

No se asume que usar más goroutines será siempre mejor. La comparación debe considerar el número de núcleos, el reparto de trabajo, el costo de sincronización y el overhead. La versión secuencial será la línea base.

## Dataset seleccionado

**SmartMeter Energy Consumption Data in London Households**, publicado en el London Datastore.

Fuente oficial: [London Datastore](https://data.london.gov.uk/dataset/smartmeter-energy-consumption-data-in-london-households-vqm0d).

| Criterio | Sustento |
| --- | --- |
| Pertinencia | Contiene consumo eléctrico residencial medido a intervalos de 30 minutos. |
| Escala | La auditoría del ZIP encontró 167,932,474 filas, muy por encima del mínimo de un millón. |
| Dimensión temporal | Permite construir series, rezagos y divisiones cronológicas. |
| Unidad de análisis | Incluye identificadores anónimos de hogares. |
| Variable objetivo | El consumo en kWh es continuo y permite plantear una regresión. |
| Reproducibilidad | La fuente es pública y el notebook documenta el procesamiento desde el ZIP. |

## Esquema original

| Columna | Descripción | Tipo normalizado |
| --- | --- | --- |
| `LCLid` | Identificador anónimo del hogar | `VARCHAR` |
| `stdorToU` | Tarifa estándar (`Std`) o por horario (`ToU`) | `VARCHAR` |
| `DateTime` | Fecha y hora de la lectura | `TIMESTAMP` |
| `KWH/hh (per half hour)` | Consumo durante media hora | `DOUBLE` |

En algunos CSV el encabezado de energía puede incluir un espacio final. El proceso normaliza el nombre y lo transforma en `energy_kwh` para evitar depender de ese detalle del archivo.

## Unidad final de análisis

El modelo no recibe directamente cada lectura de media hora. Primero se deduplican las observaciones válidas y luego se agregan por hogar y día. Solo se usan días con 48 lecturas, lo que evita comparar objetivos construidos con distinta cobertura.

El dataset final conserva:

- identificador de hogar y tarifa;
- fecha del día observado;
- consumo diario original;
- consumo diario limitado para experimentos robustos;
- variables temporales y rezagos generados sin usar información futura;
- etiqueta temporal `train`, `validation` o `test`.

## Alcance y limitaciones

- Los hogares son anónimos y no representan necesariamente a otras ciudades.
- El periodo finaliza en 2014; el objetivo académico es estudiar el método y el rendimiento, no producir una proyección operativa actual de Londres.
- Los medidores registran consumo, pero no explican por sí solos factores como ocupación, clima o características de la vivienda.
- La división temporal es obligatoria para impedir que datos futuros influyan en el entrenamiento.
- El procesamiento concurrente debe compararse bajo el mismo hardware, datos y métrica que la línea base secuencial.

