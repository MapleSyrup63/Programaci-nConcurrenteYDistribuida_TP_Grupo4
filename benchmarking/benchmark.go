package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"time"
)

type rawRun struct {
	Modo     string
	Workers  int
	TiempoMs float64
}

type resumenConfig struct {
	Modo    string
	Workers int
	MediaMs float64
	Speedup float64
}

// trimmedMean descarta el trimFraction (ej 0.1 = 10%) de los valores mas
// altos y mas bajos, y promedia el resto. Si el recorte dejaria muy pocos
// datos, no recorta (protege ejecuciones con pocas repeticiones).
func trimmedMean(times []float64, trimFraction float64) float64 {
	n := len(times)
	if n == 0 {
		return 0
	}
	sorted := append([]float64{}, times...)
	sort.Float64s(sorted)

	trim := int(float64(n) * trimFraction)
	if 2*trim >= n {
		trim = 0
	}
	kept := sorted[trim : n-trim]

	var sum float64
	for _, t := range kept {
		sum += t
	}
	return sum / float64(len(kept))
}

// RunBenchmark carga los datos UNA sola vez (ya vienen cargados en
// trainSamples) y mide solo el computo de BuildNormalEquations*, repeats
// veces por configuracion, para las cantidades de workers en workerCounts.
func RunBenchmark(trainSamples []Sample, workerCounts []int, repeats int, trimFraction float64) {
	var crudo []rawRun

	fmt.Printf("\n=== BENCHMARK: %d repeticiones por configuracion, recorte %.0f%% por extremo ===\n\n", repeats, trimFraction*100)

	// --- Secuencial (baseline, T-Secuencial) ---
	var seqTimes []float64
	for i := 0; i < repeats; i++ {
		start := time.Now()
		_, _ = BuildNormalEquationsSequential(trainSamples)
		ms := float64(time.Since(start).Microseconds()) / 1000.0
		seqTimes = append(seqTimes, ms)
		crudo = append(crudo, rawRun{"sequential", 0, ms})
		fmt.Printf("  [sequential]        corrida %2d/%d: %8.2f ms\n", i+1, repeats, ms)
	}
	seqTrimmed := trimmedMean(seqTimes, trimFraction)
	fmt.Printf("  -> Media recortada secuencial: %.2f ms\n\n", seqTrimmed)

	resumen := []resumenConfig{{"sequential", 0, seqTrimmed, 1.0}}

	// --- Concurrente, para cada cantidad de workers ---
	for _, w := range workerCounts {
		var times []float64
		for i := 0; i < repeats; i++ {
			start := time.Now()
			_, _ = BuildNormalEquationsConcurrent(trainSamples, w)
			ms := float64(time.Since(start).Microseconds()) / 1000.0
			times = append(times, ms)
			crudo = append(crudo, rawRun{"concurrent", w, ms})
			fmt.Printf("  [concurrent w=%2d]   corrida %2d/%d: %8.2f ms\n", w, i+1, repeats, ms)
		}
		trimmed := trimmedMean(times, trimFraction)
		speedup := seqTrimmed / trimmed
		resumen = append(resumen, resumenConfig{"concurrent", w, trimmed, speedup})
		fmt.Printf("  -> Media recortada (w=%d): %.2f ms | Speedup: %.2fx\n\n", w, trimmed, speedup)
	}

	guardarCrudo("benchmark_raw.csv", crudo)
	guardarResumen("benchmark_summary.csv", resumen)
}

func guardarCrudo(path string, datos []rawRun) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("No se pudo guardar %s: %v\n", path, err)
		return
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"modo", "workers", "tiempo_ms"})
	for _, r := range datos {
		w.Write([]string{r.Modo, fmt.Sprintf("%d", r.Workers), fmt.Sprintf("%.4f", r.TiempoMs)})
	}
	fmt.Printf("Guardado: %s\n", path)
}

func guardarResumen(path string, datos []resumenConfig) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("No se pudo guardar %s: %v\n", path, err)
		return
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"modo", "workers", "media_recortada_ms", "speedup"})
	for _, s := range datos {
		w.Write([]string{s.Modo, fmt.Sprintf("%d", s.Workers), fmt.Sprintf("%.4f", s.MediaMs), fmt.Sprintf("%.4f", s.Speedup)})
	}
	fmt.Printf("Guardado: %s\n", path)
}
