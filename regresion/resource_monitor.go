package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"runtime"
)

type resourceSample struct {
	Modo         string
	Workers      int
	AllocMB      float64
	TotalAllocMB float64
	SysMB        float64
	NumGC        uint32
}

func bytesToMB(b uint64) float64 {
	return float64(b) / (1024 * 1024)
}

// MeasureResources corre UNA vez cada configuracion (secuencial y cada
// cantidad de workers) y registra memoria asignada y recolecciones de
// basura (GC) antes/despues de cada corrida. El objetivo es identificar
// el "punto de equilibrio": a partir de que cantidad de workers el
// costo de memoria/GC empieza a crecer sin que el tiempo siga bajando
// en proporcion (ver punto 10).
func MeasureResources(trainSamples []Sample, workerCounts []int) {
	var resultados []resourceSample

	medir := func(modo string, workers int, fn func()) {
		runtime.GC() // limpiar basura de la corrida anterior antes de medir

		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		fn()

		runtime.ReadMemStats(&after)

		resultados = append(resultados, resourceSample{
			Modo:         modo,
			Workers:      workers,
			AllocMB:      bytesToMB(after.Alloc),
			TotalAllocMB: bytesToMB(after.TotalAlloc - before.TotalAlloc),
			SysMB:        bytesToMB(after.Sys),
			NumGC:        after.NumGC - before.NumGC,
		})
	}

	fmt.Println("\n=== ANALISIS DE RECURSOS DE COMPUTO ===")

	medir("sequential", 0, func() {
		_, _ = BuildNormalEquationsSequential(trainSamples)
	})

	for _, w := range workerCounts {
		ww := w
		medir("concurrent", ww, func() {
			_, _ = BuildNormalEquationsConcurrent(trainSamples, ww)
		})
	}

	for _, r := range resultados {
		fmt.Printf("  modo=%-11s workers=%2d | Alloc=%7.2f MB | TotalAlloc(delta)=%7.2f MB | Sys=%7.2f MB | GC(delta)=%d\n",
			r.Modo, r.Workers, r.AllocMB, r.TotalAllocMB, r.SysMB, r.NumGC)
	}

	guardarRecursos("resource_usage.csv", resultados)
}

func guardarRecursos(path string, datos []resourceSample) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("No se pudo guardar %s: %v\n", path, err)
		return
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"modo", "workers", "alloc_mb", "total_alloc_delta_mb", "sys_mb", "num_gc_delta"})
	for _, r := range datos {
		w.Write([]string{
			r.Modo,
			fmt.Sprintf("%d", r.Workers),
			fmt.Sprintf("%.2f", r.AllocMB),
			fmt.Sprintf("%.2f", r.TotalAllocMB),
			fmt.Sprintf("%.2f", r.SysMB),
			fmt.Sprintf("%d", r.NumGC),
		})
	}
	fmt.Printf("Guardado: %s\n", path)
}
