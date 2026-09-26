package main

import "sync"

// newMatrix crea una matriz n x n inicializada en ceros.
func newMatrix(n int) [][]float64 {
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
	}
	return m
}

// accumulate suma la contribucion de UNA fila a xtx y xty (in-place).
func accumulate(xtx [][]float64, xty []float64, s Sample) {
	n := len(s.X)
	for i := 0; i < n; i++ {
		xty[i] += s.X[i] * s.Y
		for j := 0; j < n; j++ {
			xtx[i][j] += s.X[i] * s.X[j]
		}
	}
}

// BuildNormalEquationsSequential recorre TODAS las filas en un solo hilo.
func BuildNormalEquationsSequential(samples []Sample) ([][]float64, []float64) {
	n := NumFeatures()
	xtx := newMatrix(n)
	xty := make([]float64, n)

	for _, s := range samples {
		accumulate(xtx, xty, s)
	}
	return xtx, xty
}

// BuildNormalEquationsConcurrent reparte samples en numWorkers bloques.
// Cada goroutine acumula su XtX/Xty parcial de forma completamente
// independiente (sin tocar memoria compartida); solo al terminar su
// bloque combina su resultado parcial con el acumulador global, seccion
// protegida con sync.Mutex -- el mismo patron verificado en Promela.
func BuildNormalEquationsConcurrent(samples []Sample, numWorkers int) ([][]float64, []float64) {
	n := NumFeatures()
	globalXtX := newMatrix(n)
	globalXty := make([]float64, n)
	var mu sync.Mutex
	var wg sync.WaitGroup

	total := len(samples)
	if numWorkers < 1 {
		numWorkers = 1
	}
	chunkSize := (total + numWorkers - 1) / numWorkers

	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		if start >= total {
			break
		}
		end := start + chunkSize
		if end > total {
			end = total
		}

		wg.Add(1)
		go func(chunk []Sample) {
			defer wg.Done()

			// Acumulador LOCAL de este worker (sin compartir memoria).
			localXtX := newMatrix(n)
			localXty := make([]float64, n)
			for _, s := range chunk {
				accumulate(localXtX, localXty, s)
			}

			// Unica seccion critica: combinar el parcial con el global.
			mu.Lock()
			for i := 0; i < n; i++ {
				globalXty[i] += localXty[i]
				for j := 0; j < n; j++ {
					globalXtX[i][j] += localXtX[i][j]
				}
			}
			mu.Unlock()
		}(samples[start:end])
	}

	wg.Wait()
	return globalXtX, globalXty
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// SolveLinearSystem resuelve XtX * beta = Xty mediante eliminacion
// gaussiana con pivoteo parcial (matriz pequena: NumFeatures x NumFeatures).
func SolveLinearSystem(xtx [][]float64, xty []float64) []float64 {
	n := len(xty)
	a := make([][]float64, n)
	b := make([]float64, n)
	for i := 0; i < n; i++ {
		a[i] = make([]float64, n)
		copy(a[i], xtx[i])
		b[i] = xty[i]
	}

	for col := 0; col < n; col++ {
		pivot := col
		maxVal := absf(a[col][col])
		for r := col + 1; r < n; r++ {
			if absf(a[r][col]) > maxVal {
				pivot = r
				maxVal = absf(a[r][col])
			}
		}
		a[col], a[pivot] = a[pivot], a[col]
		b[col], b[pivot] = b[pivot], b[col]

		for r := col + 1; r < n; r++ {
			factor := a[r][col] / a[col][col]
			for c := col; c < n; c++ {
				a[r][c] -= factor * a[col][c]
			}
			b[r] -= factor * b[col]
		}
	}

	beta := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := b[i]
		for j := i + 1; j < n; j++ {
			sum -= a[i][j] * beta[j]
		}
		beta[i] = sum / a[i][i]
	}
	return beta
}
