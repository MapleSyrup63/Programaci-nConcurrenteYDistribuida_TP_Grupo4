# Datos del proyecto

Los datos originales y procesados no se incluyen en GitHub debido a su tamaño. El archivo comprimido de origen pesa aproximadamente 700 MB y los CSV descomprimidos ocupan varios gigabytes.

## Fuente

[SmartMeter Energy Consumption Data in London Households — London Datastore](https://data.london.gov.uk/dataset/smartmeter-energy-consumption-data-in-london-households-vqm0d)

## Uso

1. Descargar el ZIP desde la fuente oficial o conservarlo en Google Drive.
2. Abrir `notebooks/Grupo4_PCD_Preprocesamiento.ipynb` en Google Colab.
3. Seleccionar el ZIP exacto con el selector del notebook.
4. Ejecutar las celdas en orden.
5. Guardar los resultados pesados en Drive, no dentro del repositorio.

## Salidas esperadas

El notebook genera, entre otros resultados:

- Parquet intermedios por fragmento;
- dataset diario limpio;
- divisiones temporales de entrenamiento, validación y prueba;
- versión original y versión limitada del objetivo;
- tablas CSV de auditoría.

Las rutas exactas se imprimen al final del notebook. Los patrones `*.zip`, `*.parquet`, `*.csv.gz` y las carpetas `data/raw/` y `data/processed/` están ignorados por Git.

No publicar copias innecesarias de los datos. Para que otra persona reproduzca el trabajo se deben versionar el código, los parámetros, la documentación y las auditorías pequeñas, no el dataset completo.

