package main

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Sample representa una fila lista para el modelo: X ya incluye el
// intercepto (posicion 0 = 1.0) seguido de las features en el orden
// de featureColumns.
type Sample struct {
	X []float64
	Y float64
}

// featureColumns son las columnas de model_prep.go usadas como
// predictores. Se excluyen final_upper_cap y target_was_capped por ser
// metadata de la limpieza (usarlas seria fuga de informacion).
var featureColumns = []string{
	"tariff_tou", "is_weekend",
	"dow_sin", "dow_cos", "month_sin", "month_cos",
	"day_index", "lag_1_kwh", "lag_7_kwh",
	"rolling_mean_7_kwh", "rolling_std_7_kwh",
}

const targetColumn = "target_daily_kwh_capped"
const splitColumn = "dataset_split"

// NumFeatures incluye el intercepto.
func NumFeatures() int { return len(featureColumns) + 1 }

// LoadSamples lee el CSV (.csv o .csv.gz) generado por model_prep.go y
// devuelve solo las filas cuyo dataset_split coincide con split
// ("train", "validation" o "test").
func LoadSamples(path, split string) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir %s: %w", path, err)
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("no se pudo descomprimir %s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}

	reader := csv.NewReader(r)
	reader.ReuseRecord = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el encabezado: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}

	requeridas := append(append([]string{}, featureColumns...), targetColumn, splitColumn)
	for _, col := range requeridas {
		if _, ok := idx[col]; !ok {
			return nil, fmt.Errorf("columna esperada no encontrada en el CSV: %s", col)
		}
	}

	var samples []Sample
	for {
		row, err := reader.Read()
		if err != nil {
			break // EOF u otro error -> fin de la carga
		}
		if row[idx[splitColumn]] != split {
			continue
		}

		x := make([]float64, NumFeatures())
		x[0] = 1.0 // intercepto
		valido := true
		for i, col := range featureColumns {
			v, perr := strconv.ParseFloat(row[idx[col]], 64)
			if perr != nil {
				valido = false
				break
			}
			x[i+1] = v
		}
		if !valido {
			continue
		}

		y, perr := strconv.ParseFloat(row[idx[targetColumn]], 64)
		if perr != nil {
			continue
		}

		samples = append(samples, Sample{X: x, Y: y})
	}

	return samples, nil
}
