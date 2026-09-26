package main

import (
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

func main() {
	inputPath := flag.String("input", "lcl_modelo_go.csv.gz", "ruta al dataset de modelado (salida de model_prep.go)")
	mode := flag.String("mode", "sequential", "sequential | concurrent | benchmark")
	workers := flag.Int("workers", 4, "numero de goroutines (solo modo concurrent)")
	workerList := flag.String("workerlist", "1,2,4,8,16", "lista de cantidades de workers a probar (solo modo benchmark)")
	repeats := flag.Int("repeats", 20, "repeticiones por configuracion (solo modo benchmark)")
	trim := flag.Float64("trim", 0.1, "fraccion a recortar por extremo, ej 0.1 = 10% (solo modo benchmark)")
	flag.Parse()

	fmt.Printf("Cargando datos de entrenamiento desde %s...\n", *inputPath)
	trainSamples, err := LoadSamples(*inputPath, "train")
	if err != nil {
		log.Fatalf("Error cargando train: %v", err)
	}
	fmt.Printf("Train: %d filas cargadas.\n", len(trainSamples))

	valSamples, err := LoadSamples(*inputPath, "validation")
	if err != nil {
		log.Fatalf("Error cargando validation: %v", err)
	}
	fmt.Printf("Validation: %d filas cargadas.\n", len(valSamples))

	if *mode == "benchmark" {
		var counts []int
		for _, s := range strings.Split(*workerList, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				log.Fatalf("workerlist invalida: %q no es un numero", s)
			}
			counts = append(counts, n)
		}
		RunBenchmark(trainSamples, counts, *repeats, *trim)
		return
	}

	if *mode == "resources" {
		var counts []int
		for _, s := range strings.Split(*workerList, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				log.Fatalf("workerlist invalida: %q no es un numero", s)
			}
			counts = append(counts, n)
		}
		MeasureResources(trainSamples, counts)
		return
	}

	var xtx [][]float64
	var xty []float64

	start := time.Now()
	switch *mode {
	case "sequential":
		xtx, xty = BuildNormalEquationsSequential(trainSamples)
	case "concurrent":
		xtx, xty = BuildNormalEquationsConcurrent(trainSamples, *workers)
	default:
		log.Fatalf("modo no reconocido: %s (usa sequential, concurrent o benchmark)", *mode)
	}
	elapsed := time.Since(start)

	beta := SolveLinearSystem(xtx, xty)

	rmseTrain, r2Train := Evaluate(beta, trainSamples)
	rmseVal, r2Val := Evaluate(beta, valSamples)

	fmt.Println("\n=== RESULTADOS ===")
	fmt.Printf("Modo: %s", *mode)
	if *mode == "concurrent" {
		fmt.Printf(" (workers=%d)", *workers)
	}
	fmt.Println()
	fmt.Printf("Tiempo de acumulacion XtX/Xty: %v\n", elapsed)
	fmt.Printf("Coeficientes (beta): %v\n", beta)
	fmt.Printf("Train      -> RMSE: %.4f | R2: %.4f\n", rmseTrain, r2Train)
	fmt.Printf("Validation -> RMSE: %.4f | R2: %.4f\n", rmseVal, r2Val)
}
