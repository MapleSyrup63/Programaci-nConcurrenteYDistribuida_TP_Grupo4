# Predicción concurrente del consumo energético urbano

Trabajo del curso **1ACC0065 – Programación Concurrente y Distribuida** (UPC, 2026-20), Grupo 4.

El proyecto predice el consumo eléctrico diario de hogares urbanos mediante **Regresión Lineal Múltiple** sobre el dataset público *SmartMeter Energy Consumption Data in London Households*. Además, compara una implementación **secuencial** y otra **concurrente** en Go, usando solo la biblioteca estándar. El caso se vincula con el **ODS 11: Ciudades y comunidades sostenibles**, por su aplicación en la planificación de la demanda energética urbana.

## Integrantes

| Código | Integrante | Usuario GitHub |
| --- | --- | --- |
| U20231B834 | Rodrigo Alonso Gamero Vera | `rodrigo-g08` |
| U202210690 | Rodrigo Alonso Ramírez Cesti | `MapleSyrup63` |
| U202218729 | Sebastian Timana Mendoza | `basTM2502` |

## Estado del trabajo

| Entrega | Contenido | Estado |
| --- | --- | --- |
| PC1 (semana 3) | Estado del arte, caso de uso, dataset y limpieza | ✅ `cleaning/` |
| PC2 (semana 5) | Modelado en Promela | ✅ `promela/` |
| | Regresión secuencial y concurrente en Go | ✅ `regresion/` |
| | Speedup con media recortada y análisis de recursos | ✅ `regresion/*.csv` y `notebooks/Grupo4_PCD_PC2.ipynb` |
| TP (semana 7) | Verificación formal en Spin, informe de GAPs con IA, video | ⏳ Pendiente |

## Estructura del repositorio

```text
.
├── README.md
├── CONTRIBUTING.md                  # flujo Git Flow y convención de commits
├── cleaning/                        # PC1: limpieza concurrente en Go
│   ├── clean_daily.go               # ZIP → lcl_diario_go.csv (Worker Pool + k-way merge)
│   ├── model_prep.go                # diario → lcl_modelo_go.csv.gz (winsorización y variables)
│   ├── audit_cleaning_go.csv        # auditoría de la limpieza
│   ├── audit_model_prep_go.csv      # auditoría del dataset de modelo
│   └── Grupo4_PCD_GO_Colab.ipynb    # ejecución de la limpieza en Colab
├── data/
│   └── README.md                    # cómo obtener los datos (los CSV no se versionan)
├── docs/
│   ├── CASO_USO_DATASET.md          # sustento del caso de uso y del dataset
│   └── PREPROCESAMIENTO.md          # detalle técnico de la limpieza
├── notebooks/
│   └── Grupo4_PCD_PC2.ipynb         # Promela, ejecución de regresion/, benchmark y análisis
├── promela/                         # PC2: modelo de sincronización
│   ├── race_condition.pml           # sin protección → Spin encuentra la condición de carrera
│   └── race_condition_fixed.pml     # con mutex → 0 errores
└── regresion/                       # PC2: regresión lineal secuencial y concurrente (un solo paquete Go)
    ├── go.mod
    ├── main.go                      # modos: sequential, concurrent, benchmark, resources
    ├── data.go                      # carga del CSV por dataset_split
    ├── normal_eq.go                 # XᵀX y Xᵀy secuencial/concurrente y resolución 12×12
    ├── metrics.go                   # RMSE y R²
    ├── benchmark.go                 # repeticiones y media recortada
    ├── resource_monitor.go          # memoria y ciclos de GC
    ├── benchmark_raw.csv            # cada medición
    ├── benchmark_summary.csv        # media recortada y speedup por configuración
    └── resource_usage.csv           # uso de memoria y GC por configuración
```

## Dataset

- **Fuente:** [London Datastore — SmartMeter Energy Consumption Data in London Households](https://data.london.gov.uk/dataset/smartmeter-energy-consumption-data-in-london-households-vqm0d).
- **Volumen de origen:** 167 932 474 lecturas de media hora en 168 CSV, dentro de un ZIP de unos 700 MB.
- **Periodo:** del 23 de noviembre de 2011 al 28 de febrero de 2014.
- **Dataset de modelo:** 3 285 438 filas hogar-día de 5 559 hogares, sin nulos ni duplicados.

| División | Fechas | Filas | Hogares |
| --- | --- | ---: | ---: |
| Entrenamiento | 2011-12-01 a 2013-09-30 | 2 559 364 | 5 556 |
| Validación | 2013-10-01 a 2013-12-31 | 452 163 | 5 214 |
| Prueba | 2014-01-01 a 2014-02-27 | 273 911 | 5 104 |

Los datos no se suben a GitHub por su tamaño; ver [data/README.md](data/README.md). El detalle de la limpieza, los duplicados, los nulos y la winsorización con 3×IQR está en [docs/PREPROCESAMIENTO.md](docs/PREPROCESAMIENTO.md).

## Etapa 1 — Limpieza concurrente en Go (PC1)

`clean_daily.go` recorre el ZIP en *streaming*. Un **Worker Pool** (canal de trabajos y `sync.WaitGroup`) procesa los 168 fragmentos en paralelo. Cada worker genera agregados parciales por hogar-día, y un **k-way merge** con `container/heap` une los días que quedan repartidos entre fragmentos. Después, `model_prep.go` aplica la winsorización aprendida solo con *train* y construye los rezagos y las variables de calendario.

Cada programa tiene su propia función `main`, así que se ejecutan por separado:

```bash
cd cleaning
go run clean_daily.go -zip "/ruta/Partitioned LCL Data.zip" -workers 4 -out ../data/lcl_diario_go.csv   # Go 1.21 o superior
go run model_prep.go  -in ../data/lcl_diario_go.csv -out ../data/lcl_modelo_go.csv.gz
```

## Etapa 2 — Regresión lineal secuencial vs. concurrente (PC2)

**Modelo:** $\hat{\beta} = (X^\top X)^{-1} X^\top y$, con 11 variables más el intercepto, lo que da **12 coeficientes** y un sistema de 12×12.

Las matrices $X^\top X$ y $X^\top y$ son sumas por filas, así que el cálculo se reparte entre goroutines:

| Mecanismo | Uso en `normal_eq.go` |
| --- | --- |
| Goroutines | El conjunto de entrenamiento se divide en tantos bloques como workers; cada goroutine procesa uno |
| Memoria local | Cada goroutine acumula su `XᵀX` y `Xᵀy` parcial sin compartir memoria |
| `sync.Mutex` | Protege la fusión del parcial en el acumulador global (una vez por goroutine) |
| `sync.WaitGroup` | Espera a todas las goroutines antes de resolver el sistema 12×12 por eliminación gaussiana |

Las dos versiones usan la **misma función de acumulación** (`accumulate`), así que la diferencia de tiempo se debe solo a la concurrencia.

```bash
cd regresion
go run . -mode sequential -input ../data/lcl_modelo_go.csv.gz
go run . -mode concurrent -input ../data/lcl_modelo_go.csv.gz -workers 8
go run . -mode benchmark  -input ../data/lcl_modelo_go.csv.gz -workerlist 1,2,4,8,16,32 -repeats 20 -trim 0.1
go run . -mode resources  -input ../data/lcl_modelo_go.csv.gz -workerlist 1,2,4,8,16,32
go run -race . -mode concurrent -input ../data/lcl_modelo_go.csv.gz -workers 8   # detector de carreras
```

**Resultado del modelo**, idéntico en ambas versiones:

| Conjunto | RMSE (kWh) | R² |
| --- | ---: | ---: |
| Entrenamiento | 3.2031 | 0.8683 |
| Validación | 3.4476 | 0.8618 |

## Verificación de la sincronización (Promela / Spin)

| Modelo | Resultado de Spin 6.5.2 |
| --- | --- |
| `race_condition.pml` | `assertion violated`, `errors: 1`: dos workers leen el mismo valor del acumulador y se pierde un aporte (termina en 268 640 en vez de 367 290 kWh). |
| `race_condition_fixed.pml` | `errors: 0`: 317 estados explorados, sin estados finales inválidos y sin código no alcanzado. |

```bash
cd promela
spin -a race_condition_fixed.pml && gcc -o pan pan.c && ./pan
```

Sobre el código real, `go run -race` no reporta condiciones de carrera.

## Rendimiento

Protocolo de medición (`benchmark.go`):
- 20 repeticiones por configuración: secuencial y 1, 2, 4, 8, 16 y 32 workers.
- Media recortada al 10 %: se descartan las 2 mediciones más altas y las 2 más bajas.
- Solo se mide la construcción de `XᵀX` y `Xᵀy`; la carga del CSV queda fuera.

Las mediciones están en `regresion/benchmark_raw.csv` y `regresion/benchmark_summary.csv`, y la memoria y el GC en `regresion/resource_usage.csv`. La tabla estadística (desviación estándar, eficiencia, Karp–Flatt) y los gráficos se generan con [notebooks/Grupo4_PCD_PC2.ipynb](notebooks/Grupo4_PCD_PC2.ipynb). El análisis completo de speedup, escalabilidad y trade-offs está en el informe de la PC2.

## Flujo de trabajo

`main` guarda las entregas, `develop` integra los cambios y cada aporte entra por una rama propia mediante un Pull Request hacia `develop`. Antes de cada entrega se integra `develop` en `main`. El detalle está en [CONTRIBUTING.md](CONTRIBUTING.md).
