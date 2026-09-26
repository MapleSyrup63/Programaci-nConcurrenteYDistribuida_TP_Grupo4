# Datos del proyecto

Los datos originales y procesados **no se versionan** en GitHub por su tamaño: el ZIP de origen pesa unos 700 MB, `lcl_diario_go.csv` unos 300 MB y `lcl_modelo_go.csv` unos 700 MB (110 MB comprimido). GitHub, además, rechaza archivos de más de 100 MB.

## Fuente

[SmartMeter Energy Consumption Data in London Households — London Datastore](https://data.london.gov.uk/dataset/smartmeter-energy-consumption-data-in-london-households-vqm0d). Se usa el archivo *Partitioned LCL Data* (168 CSV).

## Cómo generarlos

| Paso | Programa | Salida |
| --- | --- | --- |
| 1 | `cleaning/clean_daily.go` | `lcl_diario_go.csv` (3 510 403 filas hogar-día) |
| 2 | `cleaning/model_prep.go` | `lcl_modelo_go.csv.gz` (3 285 438 filas para el modelo) |

También puede ejecutarse todo en Colab con `cleaning/Grupo4_PCD_GO_Colab.ipynb`. Las salidas quedan en la carpeta de Drive `Grupo4_PCD_GO`, que es la que lee `notebooks/Grupo4_PCD_PC2.ipynb`.

## Uso local

Guarde los archivos generados en esta carpeta (`data/`) para ejecutar los comandos del README principal. El `.gitignore` excluye `data/*.csv`, `*.csv.gz` y `*.zip`, así que no se subirán por error.
