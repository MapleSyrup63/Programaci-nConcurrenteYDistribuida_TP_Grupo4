Informe de Auditoría Técnica — Grupo 4
Predicción concurrente del consumo energético urbano
Campo	Detalle
Repositorio	Programaci-nConcurrenteYDistribuidaTPGrupo4
Rama consultada	main
Lenguajes	Go, Promela
Dominio	Programación concurrente y distribuida, procesamiento de datos y regresión lineal
Tipo de auditoría	Inspección estática de código y documentación
Modelo recomendado	GPT-6, sujeto a disponibilidad en el entorno de ejecución
Estado	Preliminar; pendiente de validación dinámica
Fecha	10 de octubre de 2026
1. Resumen ejecutivo

El proyecto implementa un pipeline de procesamiento de datos energéticos y una comparación de regresión lineal múltiple secuencial frente a concurrente mediante goroutines de Go. Incluye un modelo Promela para estudiar condiciones de carrera y mediciones de rendimiento.

La revisión de los archivos disponibles identifica tres brechas prioritarias:

Modelo formal inconsistente: los archivos race_condition.pml y race_condition_fixed.pml presentan marcadores de conflicto Git sin resolver.
Robustez numérica insuficiente: el solucionador de ecuaciones normales no verifica explícitamente pivotes nulos o numéricamente inestables.
Integridad de datos insuficientemente garantizada: el cargador CSV puede confundir errores de lectura con el final del archivo y omitir registros inválidos sin generar un diagnóstico suficiente.

También se identifican oportunidades de mejora en pruebas automatizadas, validación de parámetros de benchmark, gestión de errores de escritura y reproducibilidad.

Estos resultados proceden de la inspección estática de los archivos accesibles. No se han ejecutado pruebas de compilación, análisis de carreras, Spin ni benchmarks en este proceso. Por tanto, el informe no constituye una certificación de seguridad ni una validación integral del funcionamiento.

2. Alcance y metodología
2.1 Archivos inspeccionados
README.md
CONTRIBUTING.md
regresion/main.go
regresion/data.go
regresion/normal_eq.go
regresion/benchmark.go
regresion/metrics.go
regresion/resource_monitor.go
cleaning/clean_daily.go
cleaning/model_prep.go
promela/race_condition.pml
promela/race_condition_fixed.pml
regresion/go.mod
2.2 Técnicas empleadas
Inspección estática de código fuente.
Revisión de sincronización y uso de recursos compartidos.
Análisis de manejo de errores y validación de datos.
Evaluación de estabilidad numérica.
Revisión del diseño del benchmark.
Identificación de controles y pruebas pendientes.
2.3 Limitaciones

No se verificaron el commit SHA exacto, los resultados de ejecución actuales ni la totalidad del historial Git. Tampoco se ejecutaron pruebas dinámicas o análisis de dependencias. La ausencia de una vulnerabilidad identificada no demuestra la ausencia de vulnerabilidades.

3. Arquitectura observada

El repositorio se organiza en las siguientes etapas:

Limpieza de lecturas energéticas en Go.
Agregación diaria por hogar y generación de variables.
Construcción de matrices de regresión.
Ejecución secuencial y concurrente.
Medición de tiempo, memoria y recolección de basura.
Modelado de sincronización con Promela/Spin.

En la versión concurrente, cada goroutine acumula matrices parciales de forma local. Posteriormente, utiliza sync.Mutex para fusionar los resultados y sync.WaitGroup para esperar a los trabajadores. Este patrón reduce el acceso compartido durante el cálculo principal y constituye una decisión de diseño razonable.

4. Matriz consolidada de hallazgos
ID	Categoría	Severidad	Estado	Prioridad
PROM-001	Verificación formal	Alta	Confirmado por inspección	P1
NUM-001	Robustez numérica	Alta	Defecto potencial confirmado en el código	P1
DATA-001	Integridad de datos	Alta	Defecto de manejo de errores confirmado	P1
TEST-001	Pruebas	Media	Brecha aparente; requiere inventario completo	P2
BENCH-001	Benchmark	Media	Validación insuficiente	P2
IO-001	Manejo de errores de salida	Media	Errores de escritura parcialmente ignorados	P2
OBS-001	Observabilidad de datos	Media	Diagnóstico insuficiente de filas descartadas	P2
SEC-001	Seguridad	Pendiente	Requiere análisis especializado adicional	P2

La prioridad operativa indica el orden sugerido de atención, no una puntuación CVSS.

5. Hallazgos detallados
PROM-001 — Marcadores de conflicto en modelos Promela

Severidad: Alta
Prioridad: P1
Categoría: Integridad del modelo y verificación formal

Evidencia

promela/race_condition.pml, líneas 1–50.
promela/race_condition_fixed.pml, líneas 1–58.

Ambos archivos contienen marcadores como <<<<<<< HEAD, ======= y >>>>>>>, junto con versiones alternativas del modelo.

Impacto

Los archivos no representan modelos Promela limpios. La verificación formal documentada no puede reproducirse de forma fiable a partir de estos archivos sin resolver primero el conflicto.

Recomendación

Resolver los conflictos y conservar una única versión por archivo.
Ejecutar Spin sobre el modelo con carrera y comprobar que se detecta la violación esperada.
Ejecutar Spin sobre el modelo protegido y comprobar que no se viola la propiedad de seguridad.
Registrar versión de Spin, comandos y resultados reales.
Incorporar una comprobación automática de marcadores de conflicto en CI.

Criterio de aceptación

Ambos modelos deben procesarse sin errores sintácticos y sus resultados deben corresponder a las propiedades que pretenden demostrar.

NUM-001 — Ausencia de control de pivotes en la resolución de sistemas lineales

Severidad: Alta
Prioridad: P1
Categoría: Corrección matemática y robustez

Evidencia

Archivo: regresion/normal_eq.go, líneas 104–130.

El algoritmo de eliminación gaussiana selecciona el mayor pivote disponible en valor absoluto, pero no verifica si el pivote es cero o si el sistema está mal condicionado antes de dividir.

Impacto

Una matriz singular o casi singular puede provocar coeficientes no finitos o resultados numéricamente inestables. Esto afecta la fiabilidad de las predicciones y de las métricas de evaluación.

Recomendación

Comprobar pivotes nulos y valores no finitos.
Establecer un criterio de tolerancia adecuado a la escala numérica del problema.
Devolver errores explícitos en lugar de producir coeficientes inválidos.
Evaluar QR o SVD para mejorar la estabilidad frente a las ecuaciones normales.
Añadir pruebas para matrices singulares, casi singulares y bien condicionadas.

Criterio de aceptación

El solucionador debe detectar sistemas que no puede resolver de manera fiable y no debe presentar coeficientes no finitos como resultados válidos.

DATA-001 — Lectura CSV que oculta errores

Severidad: Alta
Prioridad: P1
Categoría: Integridad de datos

Evidencia

Archivo: regresion/data.go, líneas 72–76.

El bucle de lectura termina cuando reader.Read() devuelve cualquier error, sin diferenciar entre io.EOF y un error de lectura real. Además, los valores numéricos inválidos ocasionan que la fila se omita sin contabilizar el motivo.

Impacto

Un archivo truncado o mal formado puede generar un conjunto de entrenamiento parcial. El proceso podría continuar y producir métricas sin advertir que los resultados se calcularon con menos observaciones de las previstas.

Recomendación

Terminar normalmente solo ante io.EOF.
Propagar cualquier otro error de lectura.
Validar la longitud de las filas y los índices de las columnas.
Contabilizar filas aceptadas y descartadas por causa.
Rechazar el dataset si se incumplen umbrales de integridad configurables.
Registrar el número de observaciones utilizadas en cada partición.

Criterio de aceptación

Los errores de lectura deben ser explícitos y los descartes de datos deben quedar registrados y ser verificables.

TEST-001 — Cobertura de pruebas automatizadas no acreditada

Severidad: Media
Prioridad: P2
Categoría: Calidad y mantenibilidad

Evidencia

La estructura visible destaca código fuente, notebooks y modelos Promela, pero no permite acreditar una suite de pruebas Go completa.

Impacto

Cambios en el particionado del trabajo, la sincronización o la resolución numérica pueden introducir regresiones sin una señal automática.

Recomendación

Crear pruebas para:

Equivalencia numérica entre acumulación secuencial y concurrente.
Cero muestras y una sola muestra.
Cantidades de trabajadores cero, uno y superiores al número de muestras.
Matrices singulares y valores no finitos.
Archivos CSV vacíos, truncados o con columnas ausentes.
Errores de lectura y escritura.
Casos deterministas que reproduzcan una actualización perdida.
Propiedades invariantes del pipeline de limpieza.

Ejecutar como mínimo:

cd regresion
gofmt -w *.go
go vet ./...
go test ./...
go test -race ./...


La ejecución de gofmt -w modifica archivos; debe utilizarse solo en una copia de trabajo y no como parte de una auditoría estrictamente de solo lectura.

Criterio de aceptación

Las pruebas deben formar parte del flujo automatizado y fallar cuando se incumplan las propiedades esperadas.

BENCH-001 — Parámetros de benchmark insuficientemente validados

Severidad: Media
Prioridad: P2
Categoría: Rendimiento y reproducibilidad

Evidencia

Archivo: regresion/benchmark.go, líneas 25–42 y 48–78; regresion/main.go, líneas 30–40.

El benchmark acepta parámetros de repeticiones, trabajadores y recorte estadístico sin una validación integral de sus rangos.

Impacto

Parámetros extremos o inválidos pueden producir mediciones poco útiles, resultados engañosos o divisiones por cero al calcular el speedup.

Recomendación

Exigir un número positivo de repeticiones.
Validar el intervalo del recorte y las cantidades de trabajadores.
Rechazar configuraciones duplicadas si dificultan la interpretación.
Comprobar que los tiempos medidos sean válidos y positivos.
Registrar el entorno: versión de Go, sistema operativo, CPU y configuración.
Repetir mediciones y reportar variabilidad, no solo medias.
Comparar también la salida numérica de las configuraciones medidas.

Criterio de aceptación

Las configuraciones inválidas deben rechazarse y cada benchmark debe generar resultados trazables y estadísticamente interpretables.

IO-001 — Errores de escritura parcialmente ignorados

Severidad: Media
Prioridad: P2
Categoría: Manejo de errores

Evidencia

regresion/benchmark.go, líneas 84–113.
regresion/resource_monitor.go, líneas 67–88.
cleaning/clean_daily.go, líneas 492–537.
cleaning/model_prep.go, líneas 389–419.

Algunos caminos de salida ignoran errores de Write, o delegan en Flush sin verificar sistemáticamente su resultado en todos los puntos de escritura.

Impacto

Los CSV de resultados o auditoría pueden quedar incompletos sin que el proceso notifique el fallo de manera adecuada.

Recomendación

Comprobar errores de escritura y vaciado de buffers.
Comprobar errores de cierre cuando puedan afectar a la persistencia.
Devolver errores al llamador y finalizar con un estado distinto de cero si no se puede generar el artefacto requerido.
Utilizar archivos temporales y renombrado al finalizar correctamente para evitar presentar resultados parciales como completos.

Criterio de aceptación

Un fallo de escritura debe quedar reflejado en el resultado del proceso y no debe declararse satisfactoria la generación del artefacto.

OBS-001 — Falta de diagnóstico suficiente sobre los datos descartados

Severidad: Media
Prioridad: P2
Categoría: Calidad de datos y observabilidad

Evidencia

Archivo: regresion/data.go, líneas 82–98.

Las filas que contienen valores numéricos no convertibles se descartan sin una métrica específica desde el cargador de regresión.

Impacto

Resulta difícil diferenciar entre datos faltantes, corrupción de origen, cambios de esquema y problemas del preprocesamiento.

Recomendación

Generar métricas de registros procesados, aceptados y descartados, separadas por causa. Registrar los totales por partición y comprobar que no haya discrepancias con las expectativas documentadas.

Criterio de aceptación

El informe de ejecución debe permitir reconciliar los registros de entrada con los registros utilizados y descartados.

SEC-001 — Auditoría de seguridad pendiente

Severidad: Pendiente de evaluación
Prioridad: P2

La inspección realizada no aporta evidencia suficiente para confirmar una vulnerabilidad de seguridad explotable. El proyecto parece ser principalmente una aplicación local de procesamiento de datos, pero eso no elimina los riesgos asociados a entradas mal formadas, consumo excesivo de recursos, rutas de archivos y dependencias.

Recomendación

Revisar las dependencias y la versión efectiva de Go.
Ejecutar herramientas SAST y análisis de dependencias.
Validar archivos de entrada y límites de recursos.
Comprobar permisos de escritura y rutas de salida.
Revisar el historial y la configuración del repositorio para detectar secretos.
Documentar qué controles se ejecutaron y qué riesgos permanecen sin verificar.

No debe interpretarse esta sección como una certificación de que el repositorio es seguro.

6. Evaluación de concurrencia

El diseño observado en BuildNormalEquationsConcurrent mantiene acumuladores locales por goroutine y fusiona los resultados mediante un mutex. El WaitGroup se utiliza para esperar a que los trabajadores terminen.

No se ha identificado una carrera evidente en esa ruta mediante la inspección estática realizada. Sin embargo, esta observación no sustituye a la ejecución de go test -race ./... ni a pruebas de equivalencia bajo diferentes particiones.

Las siguientes propiedades deberían convertirse en pruebas explícitas:

Todos los bloques de muestras se procesan exactamente una vez.
Ninguna goroutine modifica simultáneamente el acumulador global fuera de la sección crítica.
La función solo devuelve después de que todos los trabajadores hayan finalizado.
El resultado concurrente coincide con el secuencial dentro de una tolerancia numérica documentada.
El comportamiento es correcto con cero muestras y con valores extremos de numWorkers.

Debe tenerse en cuenta que las operaciones de coma flotante pueden producir pequeñas diferencias por el orden de acumulación. La equivalencia no debe exigir necesariamente igualdad bit a bit.

7. Evaluación de calidad y arquitectura
Fortalezas observadas
Separación de responsabilidades entre carga, acumulación, resolución y métricas.
Uso de acumuladores locales para reducir la contención.
Uso explícito de mecanismos estándar de sincronización de Go.
Procesamiento concurrente de fragmentos en el pipeline de limpieza.
Generación de archivos de auditoría y resultados de benchmark.
Documentación del flujo de trabajo y de los comandos de ejecución.
Mejoras recomendadas
Introducir una política uniforme de propagación de errores.
Separar las funciones de cálculo de las funciones de entrada/salida.
Incorporar pruebas automatizadas a cada etapa del pipeline.
Documentar invariantes matemáticas y de concurrencia.
Centralizar la validación de parámetros.
Evitar que las herramientas de evaluación acepten resultados parciales sin advertencia.
8. Plan de remediación
Prioridad	Acción	Resultado esperado
P1	Resolver los conflictos de Promela	Modelos formales sintácticamente válidos
P1	Corregir el manejo de errores CSV	Datos incompletos detectados explícitamente
P1	Validar pivotes y estabilidad numérica	Fallos matemáticos detectados de forma controlada
P2	Añadir pruebas unitarias y de concurrencia	Regresiones detectadas automáticamente
P2	Validar parámetros de benchmark	Mediciones reproducibles y válidas
P2	Corregir propagación de errores de salida	Artefactos completos o fallos explícitos
P2	Mejorar métricas de calidad de datos	Trazabilidad de descartes y totales
P2	Ejecutar análisis de seguridad y dependencias	Evidencia documentada de los controles
9. Conclusiones

El repositorio presenta una estructura coherente para un proyecto académico de programación concurrente, con separación de etapas y uso explícito de goroutines, mutexes y modelos Promela.

Las principales brechas identificadas se relacionan con la reproducibilidad de la verificación formal, la estabilidad numérica y la integridad del procesamiento de datos. La corrección de estas áreas debería preceder a la ampliación del alcance del proyecto.

Antes de cerrar la auditoría, es necesario ejecutar las pruebas de Go, el detector de carreras, Spin y los análisis de seguridad disponibles. También debe fijarse el commit exacto y conservarse evidencia de los comandos y resultados.

El estado final de seguridad, calidad y concurrencia permanece condicionado a esas comprobaciones.

10. Referencias del repositorio
README.md
CONTRIBUTING.md
normal_eq.go
data.go
benchmark.go
race_condition.pml
raceconditionfixed.pml
