package main

import "math"

// Predict calcula la prediccion del modelo para una fila.
func Predict(beta []float64, x []float64) float64 {
	var sum float64
	for i := range beta {
		sum += beta[i] * x[i]
	}
	return sum
}

// Evaluate calcula RMSE y R2 del modelo sobre un conjunto de muestras.
func Evaluate(beta []float64, samples []Sample) (rmse, r2 float64) {
	if len(samples) == 0 {
		return 0, 0
	}

	var meanY float64
	for _, s := range samples {
		meanY += s.Y
	}
	meanY /= float64(len(samples))

	var sse, sst float64
	for _, s := range samples {
		pred := Predict(beta, s.X)
		e := s.Y - pred
		sse += e * e
		d := s.Y - meanY
		sst += d * d
	}

	rmse = math.Sqrt(sse / float64(len(samples)))
	if sst > 0 {
		r2 = 1 - sse/sst
	}
	return rmse, r2
}
