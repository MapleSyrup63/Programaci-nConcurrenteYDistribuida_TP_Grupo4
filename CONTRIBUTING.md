# Guía de contribución

## Ramas

- `main`: versiones entregables.
- `develop`: integración de la etapa actual.
- `feature/<descripcion>`: trabajo concreto de un integrante.
- `release/<entrega>`: revisión final antes de pasar a `main`.

Cada rama debe salir de una versión actualizada de `develop`:

```powershell
git switch develop
git pull origin develop
git switch -c feature/nombre-breve
```

## Commits

Usar mensajes cortos que indiquen el tipo y el cambio realizado:

```text
docs: documentar selección del dataset
data: añadir auditoría del preprocesamiento
feat: implementar regresión lineal secuencial
test: validar cálculo del error cuadrático medio
fix: corregir lectura de fechas
```

Antes de confirmar:

```powershell
git status
git diff --check
git diff --staged
```

No se deben confirmar el ZIP original, CSV masivos, Parquet, credenciales, rutas personales ni archivos temporales.

## Pull Requests

1. Subir la rama con `git push -u origin feature/nombre-breve`.
2. Abrir un Pull Request hacia `develop`.
3. Describir qué cambió, cómo se verificó y qué archivo evidencia el aporte.
4. Solicitar revisión de otro integrante.
5. Corregir observaciones en la misma rama.
6. Integrar conservando el historial. Para esta entrega se recomienda **Create a merge commit**, no `Squash and merge`, porque la rúbrica solicita evidencia de participación.

La autoría debe corresponder al trabajo real. No se comparten cuentas, no se cambia el autor de un commit ajeno y no se crean commits vacíos solo para aumentar el conteo.

