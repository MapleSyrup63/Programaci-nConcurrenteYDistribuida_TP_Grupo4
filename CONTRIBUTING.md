# Guía de contribución

## Ramas (Git Flow)

| Rama | Uso |
| --- | --- |
| `main` | Versiones entregadas (PC1, PC2, TP). Solo recibe merges desde `release/*`. |
| `develop` | Integración de la etapa actual. Recibe los Pull Requests de las ramas `feature/*`. |
| `feature/<descripcion>` | Trabajo concreto de un integrante. Sale de `develop` actualizado. |
| `release/<entrega>` | Revisión final antes de pasar a `main` (por ejemplo, `release/pc2`). |

```powershell
git switch develop
git pull origin develop
git switch -c feature/nombre-breve
```

## Commits

Mensajes cortos que indiquen el tipo y el cambio:

```text
feat: implementar regresión concurrente con Worker Pool
fix: corregir lectura de fechas
docs: actualizar README para la PC2
refactor: organizar el código por etapas
test: verificar ausencia de carreras con go run -race
chore: ignorar binarios de Spin y Go
```

Antes de confirmar:

```powershell
git status
git diff --staged
```

No se deben confirmar el ZIP original, los CSV masivos, archivos Parquet, binarios (`pan`, `*.exe`), credenciales ni rutas personales.

## Pull Requests

1. Subir la rama: `git push -u origin feature/nombre-breve`.
2. Abrir el Pull Request con base **`develop`**, no `main`.
3. Describir qué cambió, cómo se verificó y qué evidencia lo respalda.
4. Pedir la revisión de otro integrante, que debe aprobarlo antes del merge.
5. Integrar con **Create a merge commit**, no con *Squash*, para conservar la autoría de cada commit.
6. Al cerrar una entrega: crear `release/<entrega>` desde `develop`, abrir un PR hacia `main` y etiquetar la versión (`v1-pc1`, `v2-pc2`, `v3-tp`).

La autoría debe corresponder al trabajo real: no se comparten cuentas, no se cambia el autor de commits ajenos y no se crean commits vacíos. Según el enunciado, el historial de `main` no debe editarse después de la fecha de entrega.
