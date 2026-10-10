// series.go — Análisis de series temporales del modelo de Regresión Lineal (TP, Grupo 4).
//
// El modelo de la PC2 predice el consumo del día t con rezagos (lag_1, lag_7),
// estadísticas móviles de 7 días y variables de calendario: es un modelo
// autorregresivo con variables exógenas (ARX) a un día de horizonte.
// Este programa lo evalúa como pronóstico:
//
//	-mode pronostico   ajusta con train+validation y pronostica el conjunto test
//	                   (ene–feb 2014); compara con líneas base ingenuas y
//	                   diagnostica los residuos (ACF por hogar y Durbin-Watson).
//	-mode walkforward  validación walk-forward mensual (origen móvil, ventana
//	                   expansiva): un ajuste por mes, repartidos entre un Worker
//	                   Pool. Compara la versión secuencial con la concurrente.
//	-mode benchmark    speedup del walk-forward con media recortada.
//	-mode tendencia    tendencia y estacionalidad de la serie diaria (ver tendencia.go).
//
// Solo biblioteca estándar de Go.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mismas 11 variables que regresion.go (PC2) + intercepto = P = 12 coeficientes.
var featureCols = []string{
	"tariff_tou", "is_weekend", "dow_sin", "dow_cos", "month_sin", "month_cos",
	"day_index", "lag_1_kwh", "lag_7_kwh", "rolling_mean_7_kwh", "rolling_std_7_kwh",
}

const targetCol = "target_daily_kwh_capped"

var P = len(featureCols) + 1

// Posición en cada fila de X de las variables que usan las líneas base (+1 por el intercepto).
var (
	cLag1  = 1 + indexOf(featureCols, "lag_1_kwh")
	cLag7  = 1 + indexOf(featureCols, "lag_7_kwh")
	cMean7 = 1 + indexOf(featureCols, "rolling_mean_7_kwh")
)

// day_index = días transcurridos desde esta fecha (definido en model_prep.go, PC1).
var origen = time.Date(2011, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	splitTrain = iota
	splitValid
	splitTest
)

// Data guarda X en un solo slice contiguo, ORDENADO POR DÍA. Así, la ventana de
// entrenamiento de cualquier origen temporal es un prefijo [0, k) y el mes a
// pronosticar es un rango contiguo [k, k2): se reutiliza accumulate tal cual.
type Data struct {
	N      int
	X      []float64 // N*P, columna 0 = intercepto
	Y      []float64
	Hogar  []int32 // índice del hogar
	Dia    []int32 // day_index
	Split  []uint8
	Hogars []string // id original de cada hogar
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	panic("columna no encontrada: " + s)
}

func fecha(dia int32) time.Time { return origen.AddDate(0, 0, int(dia)) }

func diaDe(t time.Time) int32 { return int32(t.Sub(origen).Hours() / 24) }

// ---------------------------------------------------------------- carga

func loadAll(path, idCol string) (*Data, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = bufio.NewReaderSize(f, 4<<20)
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	cr := csv.NewReader(bufio.NewReaderSize(r, 4<<20))
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(strings.TrimPrefix(h, "\xef\xbb\xbf"))] = i
	}
	col := func(name string) (int, error) {
		j, ok := idx[name]
		if !ok {
			return 0, fmt.Errorf("falta la columna %q; columnas disponibles: %s", name, strings.Join(header, ", "))
		}
		return j, nil
	}
	// Columna del hogar: la indicada o la primera candidata presente.
	if idCol == "" {
		for _, c := range []string{"LCLid", "lclid", "household_id", "hogar_id", "id"} {
			if _, ok := idx[c]; ok {
				idCol = c
				break
			}
		}
		if idCol == "" {
			return nil, fmt.Errorf("no se encontró la columna del hogar; indíquela con -id. Columnas: %s", strings.Join(header, ", "))
		}
	}
	cols := make([]int, len(featureCols))
	for i, c := range featureCols {
		if cols[i], err = col(c); err != nil {
			return nil, err
		}
	}
	yi, err := col(targetCol)
	if err != nil {
		return nil, err
	}
	si, err := col("dataset_split")
	if err != nil {
		return nil, err
	}
	hi, err := col(idCol)
	if err != nil {
		return nil, err
	}
	di := 1 + indexOf(featureCols, "day_index") // posición de day_index dentro de la fila

	raw := &Data{}
	ids := map[string]int32{}
	row := make([]float64, P)
	descartadas := 0
	for {
		rec, e := cr.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		var sp uint8
		switch rec[si] {
		case "train":
			sp = splitTrain
		case "validation":
			sp = splitValid
		case "test":
			sp = splitTest
		default:
			descartadas++
			continue
		}
		row[0] = 1
		ok := true
		for i, j := range cols {
			v, e := strconv.ParseFloat(rec[j], 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				ok = false
				break
			}
			row[i+1] = v
		}
		y, e := strconv.ParseFloat(rec[yi], 64)
		if !ok || e != nil || math.IsNaN(y) {
			descartadas++ // a diferencia de la PC2, las filas descartadas se cuentan
			continue
		}
		h, existe := ids[rec[hi]]
		if !existe {
			h = int32(len(raw.Hogars))
			ids[rec[hi]] = h
			raw.Hogars = append(raw.Hogars, rec[hi])
		}
		raw.X = append(raw.X, row...)
		raw.Y = append(raw.Y, y)
		raw.Hogar = append(raw.Hogar, h)
		raw.Dia = append(raw.Dia, int32(math.Round(row[di])))
		raw.Split = append(raw.Split, sp)
		raw.N++
	}
	if descartadas > 0 {
		fmt.Printf("Aviso: %d filas descartadas por valores no numéricos o split desconocido\n", descartadas)
	}
	return ordenarPorDia(raw), nil
}

// ordenarPorDia reordena las filas por (día, hogar) con un ordenamiento estable.
func ordenarPorDia(d *Data) *Data {
	perm := make([]int, d.N)
	for i := range perm {
		perm[i] = i
	}
	sort.SliceStable(perm, func(a, b int) bool {
		if d.Dia[perm[a]] != d.Dia[perm[b]] {
			return d.Dia[perm[a]] < d.Dia[perm[b]]
		}
		return d.Hogar[perm[a]] < d.Hogar[perm[b]]
	})
	o := &Data{N: d.N, X: make([]float64, len(d.X)), Y: make([]float64, d.N),
		Hogar: make([]int32, d.N), Dia: make([]int32, d.N), Split: make([]uint8, d.N), Hogars: d.Hogars}
	for i, j := range perm {
		copy(o.X[i*P:i*P+P], d.X[j*P:j*P+P])
		o.Y[i], o.Hogar[i], o.Dia[i], o.Split[i] = d.Y[j], d.Hogar[j], d.Dia[j], d.Split[j]
	}
	return o
}

// primerIndice devuelve la primera fila con Dia >= dia (búsqueda binaria; Data está ordenado).
func primerIndice(d *Data, dia int32) int {
	return sort.Search(d.N, func(i int) bool { return d.Dia[i] >= dia })
}

// ---------------------------------------------------------------- regresión (igual que la PC2)

func accumulate(d *Data, lo, hi int, xtx, xty []float64) {
	for r := lo; r < hi; r++ {
		x := d.X[r*P : r*P+P]
		y := d.Y[r]
		for i := 0; i < P; i++ {
			xi := x[i]
			xty[i] += xi * y
			rowI := xtx[i*P : i*P+P]
			for j := i; j < P; j++ {
				rowI[j] += xi * x[j]
			}
		}
	}
}

func mirror(xtx []float64) {
	for i := 0; i < P; i++ {
		for j := 0; j < i; j++ {
			xtx[i*P+j] = xtx[j*P+i]
		}
	}
}

func solve(A, b []float64) ([]float64, error) {
	n := len(b)
	M := make([]float64, n*(n+1))
	for i := 0; i < n; i++ {
		copy(M[i*(n+1):], A[i*n:i*n+n])
		M[i*(n+1)+n] = b[i]
	}
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(M[r*(n+1)+c]) > math.Abs(M[p*(n+1)+c]) {
				p = r
			}
		}
		if math.Abs(M[p*(n+1)+c]) < 1e-12 {
			return nil, fmt.Errorf("matriz singular en columna %d", c)
		}
		for k := 0; k <= n; k++ {
			M[c*(n+1)+k], M[p*(n+1)+k] = M[p*(n+1)+k], M[c*(n+1)+k]
		}
		for r := c + 1; r < n; r++ {
			f := M[r*(n+1)+c] / M[c*(n+1)+c]
			for k := c; k <= n; k++ {
				M[r*(n+1)+k] -= f * M[c*(n+1)+k]
			}
		}
	}
	beta := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := M[i*(n+1)+n]
		for k := i + 1; k < n; k++ {
			s -= M[i*(n+1)+k] * beta[k]
		}
		beta[i] = s / M[i*(n+1)+i]
	}
	return beta, nil
}

// fitPrefix ajusta la regresión con las filas [0, hi): todo lo anterior al origen.
func fitPrefix(d *Data, hi int) ([]float64, error) {
	xtx, xty := make([]float64, P*P), make([]float64, P)
	accumulate(d, 0, hi, xtx, xty)
	mirror(xtx)
	return solve(xtx, xty)
}

func predict(d *Data, r int, beta []float64) float64 {
	x := d.X[r*P : r*P+P]
	var s float64
	for i := 0; i < P; i++ {
		s += beta[i] * x[i]
	}
	return s
}

// ---------------------------------------------------------------- métricas de pronóstico

type Metr struct {
	N                         int
	SSE, SAE, SSMAPE, SY, SY2 float64
}

func (m *Metr) Add(y, p float64) {
	e := y - p
	m.N++
	m.SSE += e * e
	m.SAE += math.Abs(e)
	if den := math.Abs(y) + math.Abs(p); den > 0 {
		m.SSMAPE += 200 * math.Abs(e) / den
	}
	m.SY += y
	m.SY2 += y * y
}

func (m *Metr) Merge(o Metr) {
	m.N += o.N
	m.SSE += o.SSE
	m.SAE += o.SAE
	m.SSMAPE += o.SSMAPE
	m.SY += o.SY
	m.SY2 += o.SY2
}

func (m Metr) RMSE() float64  { return math.Sqrt(m.SSE / float64(m.N)) }
func (m Metr) MAE() float64   { return m.SAE / float64(m.N) }
func (m Metr) SMAPE() float64 { return m.SSMAPE / float64(m.N) }
func (m Metr) R2() float64 {
	sst := m.SY2 - m.SY*m.SY/float64(m.N)
	return 1 - m.SSE/sst
}

// Predictores: el modelo ARX y tres líneas base ingenuas de series temporales.
var nombres = []string{"ARX (regresión)", "Ingenuo: lag_1 (ayer)", "Ingenuo estacional: lag_7", "Media móvil 7 días"}

func pronosticos(d *Data, r int, beta []float64) [4]float64 {
	x := d.X[r*P : r*P+P]
	return [4]float64{predict(d, r, beta), x[cLag1], x[cLag7], x[cMean7]}
}

// ---------------------------------------------------------------- diagnóstico de residuos

// acfPorHogar calcula la autocorrelación de los residuos para los rezagos 1..maxLag,
// emparejando solo días del MISMO hogar separados exactamente k días. También
// devuelve el estadístico de Durbin-Watson con los pares de días consecutivos.
func acfPorHogar(d *Data, filas []int, res []float64, maxLag int) (acf []float64, pares []int, dw float64) {
	ord := append([]int(nil), filas...)
	sort.Slice(ord, func(a, b int) bool {
		ia, ib := ord[a], ord[b]
		if d.Hogar[ia] != d.Hogar[ib] {
			return d.Hogar[ia] < d.Hogar[ib]
		}
		return d.Dia[ia] < d.Dia[ib]
	})
	var media float64
	for _, i := range ord {
		media += res[i]
	}
	media /= float64(len(ord))
	var den float64
	for _, i := range ord {
		den += (res[i] - media) * (res[i] - media)
	}
	num := make([]float64, maxLag+1)
	pares = make([]int, maxLag+1)
	var dwNum, dwDen float64
	for p, i := range ord {
		ei := res[i] - media
		for q := p - 1; q >= 0; q-- {
			j := ord[q]
			if d.Hogar[j] != d.Hogar[i] {
				break
			}
			k := int(d.Dia[i] - d.Dia[j])
			if k > maxLag {
				break
			}
			num[k] += ei * (res[j] - media)
			pares[k]++
			if k == 1 {
				dwNum += (res[i] - res[j]) * (res[i] - res[j])
				dwDen += res[i] * res[i]
			}
		}
	}
	acf = make([]float64, maxLag+1)
	for k := 1; k <= maxLag; k++ {
		acf[k] = num[k] / den
	}
	if dwDen > 0 {
		dw = dwNum / dwDen
	}
	return
}

// ---------------------------------------------------------------- walk-forward

// Fold: se entrena con las filas [0, Lo) y se pronostica el mes [Lo, Hi).
type Fold struct {
	ID     int
	Mes    string
	Lo, Hi int
}

type FoldRes struct {
	Fold
	Metr [4]Metr // ARX y líneas base
	Err  error
}

func armarFolds(d *Data, desde string) ([]Fold, error) {
	t, err := time.Parse("2006-01", desde)
	if err != nil {
		return nil, fmt.Errorf("-desde debe tener formato AAAA-MM: %v", err)
	}
	ultimo := fecha(d.Dia[d.N-1])
	var folds []Fold
	for m := t; !m.After(ultimo); m = m.AddDate(0, 1, 0) {
		lo, hi := primerIndice(d, diaDe(m)), primerIndice(d, diaDe(m.AddDate(0, 1, 0)))
		if lo > 0 && hi > lo {
			folds = append(folds, Fold{len(folds), m.Format("2006-01"), lo, hi})
		}
	}
	if len(folds) == 0 {
		return nil, fmt.Errorf("no hay meses para evaluar desde %s", desde)
	}
	return folds, nil
}

// runFold es el MISMO trabajo para la versión secuencial y la concurrente.
func runFold(d *Data, f Fold) FoldRes {
	r := FoldRes{Fold: f}
	beta, err := fitPrefix(d, f.Lo)
	if err != nil {
		r.Err = err
		return r
	}
	for i := f.Lo; i < f.Hi; i++ {
		p := pronosticos(d, i, beta)
		for k := range p {
			r.Metr[k].Add(d.Y[i], p[k])
		}
	}
	return r
}

func walkSecuencial(d *Data, folds []Fold) ([]FoldRes, Metr) {
	res := make([]FoldRes, len(folds))
	var global Metr
	for _, f := range folds {
		res[f.ID] = runFold(d, f)
		global.Merge(res[f.ID].Metr[0])
	}
	return res, global
}

// walkConcurrente reparte los meses entre un Worker Pool:
//   - canal jobs: reparto dinámico; un worker libre toma el siguiente mes.
//   - res[f.ID]: cada fold escribe en su propia posición (sin memoria compartida).
//   - global: acumulador compartido de errores, protegido por sync.Mutex
//     (mismo protocolo verificado en Spin: worker_pool.pml).
//   - sync.WaitGroup: espera a todos los workers.
//
// Orden "desc": se envían primero los meses con más filas de entrenamiento
// (los más costosos). Es la heurística LPT (Longest Processing Time first) y
// evita que el fold más pesado empiece al final y deje a los demás workers ociosos.
func walkConcurrente(d *Data, folds []Fold, workers int, orden string) ([]FoldRes, Metr) {
	res := make([]FoldRes, len(folds))
	var global Metr
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan Fold, len(folds))

	envio := append([]Fold(nil), folds...)
	if orden == "desc" {
		sort.Slice(envio, func(a, b int) bool { return envio[a].Lo > envio[b].Lo })
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				r := runFold(d, f)
				res[f.ID] = r // posición exclusiva del fold
				mu.Lock()     // ---- sección crítica ----
				global.Merge(r.Metr[0])
				mu.Unlock()
			}
		}()
	}
	for _, f := range envio {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	return res, global
}

// ---------------------------------------------------------------- utilidades

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

func trimmedMean(xs []float64, frac float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	k := int(float64(len(s)) * frac)
	s = s[k : len(s)-k]
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

func parseInts(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if v, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func f4(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

func crearCSV(dir, nombre string, cab []string) (*os.File, *csv.Writer) {
	f, err := os.Create(filepath.Join(dir, nombre))
	if err != nil {
		fatal(err)
	}
	w := csv.NewWriter(f)
	w.Write(cab)
	return f, w
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}

func cerrar(f *os.File, w *csv.Writer) {
	w.Flush()
	if err := w.Error(); err != nil {
		fatal(err)
	}
	f.Close()
}

// ---------------------------------------------------------------- modos

func modoPronostico(d *Data, outdir string, maxLag int) {
	k := primerIndice(d, diaDe(time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC)))
	for i := k; i < d.N; i++ {
		if d.Split[i] != splitTest {
			fatal(fmt.Errorf("se esperaba que todo lo posterior al 2014-01-01 fuera test"))
		}
	}
	if k == d.N {
		fatal(fmt.Errorf("el dataset no tiene filas de test"))
	}
	beta, err := fitPrefix(d, k)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\n=== PRONÓSTICO A UN DÍA SOBRE TEST (%s a %s) ===\n",
		fecha(d.Dia[k]).Format("2006-01-02"), fecha(d.Dia[d.N-1]).Format("2006-01-02"))
	fmt.Printf("Ajuste con train+validation: %d filas | Test: %d filas\n\n", k, d.N-k)

	var m [4]Metr
	res := make([]float64, d.N) // residuos del ARX en todas las filas
	for i := 0; i < d.N; i++ {
		p := pronosticos(d, i, beta)
		res[i] = d.Y[i] - p[0]
		if i >= k {
			for j := range p {
				m[j].Add(d.Y[i], p[j])
			}
		}
	}
	f, w := crearCSV(outdir, "pronostico_test.csv",
		[]string{"predictor", "n", "rmse", "mae", "smape_pct", "r2", "mejora_rmse_vs_naive_estacional_pct"})
	fmt.Printf("%-26s %8s %8s %9s %8s %s\n", "Predictor", "RMSE", "MAE", "sMAPE %", "R2", "Mejora RMSE vs lag_7")
	for j := range m {
		mej := 100 * (1 - m[j].RMSE()/m[2].RMSE())
		fmt.Printf("%-26s %8.4f %8.4f %9.2f %8.4f %+.1f %%\n", nombres[j], m[j].RMSE(), m[j].MAE(), m[j].SMAPE(), m[j].R2(), mej)
		w.Write([]string{nombres[j], strconv.Itoa(m[j].N), f4(m[j].RMSE()), f4(m[j].MAE()), f4(m[j].SMAPE()), f4(m[j].R2()), f4(mej)})
	}
	cerrar(f, w)

	// Diagnóstico de residuos: dentro de la muestra (train+validation) y fuera (test).
	f, w = crearCSV(outdir, "acf_residuos.csv", []string{"conjunto", "lag", "acf", "pares", "banda_95"})
	fd, wd := crearCSV(outdir, "diagnostico_residuos.csv", []string{"conjunto", "n", "media_residuo", "durbin_watson", "acf_1", "acf_7", "acf_14"})
	for _, c := range []struct {
		nombre string
		lo, hi int
	}{{"ajuste", 0, k}, {"test", k, d.N}} {
		filas := make([]int, 0, c.hi-c.lo)
		var media float64
		for i := c.lo; i < c.hi; i++ {
			filas = append(filas, i)
			media += res[i]
		}
		media /= float64(len(filas))
		acf, pares, dw := acfPorHogar(d, filas, res, maxLag)
		for l := 1; l <= maxLag; l++ {
			banda := 0.0
			if pares[l] > 0 {
				banda = 1.96 / math.Sqrt(float64(pares[l]))
			}
			w.Write([]string{c.nombre, strconv.Itoa(l), f4(acf[l]), strconv.Itoa(pares[l]), f4(banda)})
		}
		a14 := 0.0
		if maxLag >= 14 {
			a14 = acf[14]
		}
		wd.Write([]string{c.nombre, strconv.Itoa(len(filas)), f4(media), f4(dw), f4(acf[1]), f4(acf[7]), f4(a14)})
		fmt.Printf("\nResiduos [%s] n=%d | media %.4f | Durbin-Watson %.3f | ACF(1) %.3f | ACF(7) %.3f\n",
			c.nombre, len(filas), media, dw, acf[1], acf[7])
	}
	cerrar(f, w)
	cerrar(fd, wd)

	// Muestra para graficar: los 3 hogares con más días en test.
	cuenta := map[int32]int{}
	for i := k; i < d.N; i++ {
		cuenta[d.Hogar[i]]++
	}
	hs := make([]int32, 0, len(cuenta))
	for h := range cuenta {
		hs = append(hs, h)
	}
	sort.Slice(hs, func(a, b int) bool {
		if cuenta[hs[a]] != cuenta[hs[b]] {
			return cuenta[hs[a]] > cuenta[hs[b]]
		}
		return hs[a] < hs[b]
	})
	if len(hs) > 3 {
		hs = hs[:3]
	}
	elegido := map[int32]bool{}
	for _, h := range hs {
		elegido[h] = true
	}
	f, w = crearCSV(outdir, "pronostico_muestra.csv", []string{"hogar", "fecha", "real", "arx", "naive_lag7"})
	for i := k; i < d.N; i++ {
		if elegido[d.Hogar[i]] {
			p := pronosticos(d, i, beta)
			w.Write([]string{d.Hogars[d.Hogar[i]], fecha(d.Dia[i]).Format("2006-01-02"), f4(d.Y[i]), f4(p[0]), f4(p[2])})
		}
	}
	cerrar(f, w)
}

func modoWalkforward(d *Data, outdir, desde string, workers int, orden string) {
	folds, err := armarFolds(d, desde)
	if err != nil {
		fatal(err)
	}
	t0 := time.Now()
	seq, gs := walkSecuencial(d, folds)
	tSeq := time.Since(t0)
	t0 = time.Now()
	conc, gc := walkConcurrente(d, folds, workers, orden)
	tConc := time.Since(t0)

	// La concurrencia no debe alterar los resultados de ningún fold.
	var dif float64
	for i := range seq {
		if seq[i].Err != nil || conc[i].Err != nil {
			fatal(fmt.Errorf("fold %s: %v %v", seq[i].Mes, seq[i].Err, conc[i].Err))
		}
		dif = math.Max(dif, math.Abs(seq[i].Metr[0].SSE-conc[i].Metr[0].SSE))
	}
	fmt.Printf("\n=== WALK-FORWARD MENSUAL (%d meses, desde %s) ===\n", len(folds), desde)
	fmt.Printf("Secuencial: %.1f ms | Concurrente (%d workers, orden %s): %.1f ms | speedup %.2fx\n",
		ms(tSeq), workers, orden, ms(tConc), ms(tSeq)/ms(tConc))
	fmt.Printf("Diferencia máxima de SSE entre versiones: %.2e | RMSE global sec %.4f / conc %.4f\n\n",
		dif, gs.RMSE(), gc.RMSE())

	f, w := crearCSV(outdir, "walkforward_meses.csv",
		[]string{"mes", "filas_entrenamiento", "filas_mes", "rmse_arx", "rmse_naive_lag1", "rmse_naive_lag7", "rmse_media7", "mae_arx", "smape_arx", "r2_arx"})
	fmt.Printf("%-8s %12s %9s %9s %9s %9s\n", "Mes", "Entrenam.", "Filas", "RMSE ARX", "RMSE lag7", "R2 ARX")
	for _, r := range seq {
		m := r.Metr
		fmt.Printf("%-8s %12d %9d %9.4f %9.4f %9.4f\n", r.Mes, r.Lo, r.Hi-r.Lo, m[0].RMSE(), m[2].RMSE(), m[0].R2())
		w.Write([]string{r.Mes, strconv.Itoa(r.Lo), strconv.Itoa(r.Hi - r.Lo), f4(m[0].RMSE()), f4(m[1].RMSE()),
			f4(m[2].RMSE()), f4(m[3].RMSE()), f4(m[0].MAE()), f4(m[0].SMAPE()), f4(m[0].R2())})
	}
	cerrar(f, w)
}

func modoBenchmark(d *Data, outdir, desde string, reps int, wlist []int, orden string) {
	folds, err := armarFolds(d, desde)
	if err != nil {
		fatal(err)
	}
	f, w := crearCSV(outdir, "walkforward_benchmark_"+orden+".csv", []string{"modo", "workers", "orden", "rep", "wall_ms", "cpu_ms", "alloc_kb", "gc"})
	configs := append([]int{0}, wlist...)
	fmt.Printf("\n=== BENCHMARK WALK-FORWARD (%d meses, %d repeticiones + 1 de calentamiento) ===\n", len(folds), reps)
	for _, nw := range configs {
		modo := "secuencial"
		if nw > 0 {
			modo = "concurrente"
		}
		ejecutar := func() {
			if nw == 0 {
				walkSecuencial(d, folds)
			} else {
				walkConcurrente(d, folds, nw, orden)
			}
		}
		ejecutar() // calentamiento, no se registra
		walls := make([]float64, 0, reps)
		for r := 1; r <= reps; r++ {
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			c0, t0 := cpuTime(), time.Now()
			ejecutar()
			wall, cpu := time.Since(t0), cpuTime()-c0
			runtime.ReadMemStats(&m1)
			walls = append(walls, ms(wall))
			w.Write([]string{modo, strconv.Itoa(nw), orden, strconv.Itoa(r), f4(ms(wall)), f4(ms(cpu)),
				strconv.FormatFloat(float64(m1.TotalAlloc-m0.TotalAlloc)/1024, 'f', 2, 64), strconv.Itoa(int(m1.NumGC - m0.NumGC))})
		}
		w.Flush()
		fmt.Printf("%-11s workers=%-3d media recortada 10%% = %9.2f ms\n", modo, nw, trimmedMean(walls, 0.10))
	}
	cerrar(f, w)
}

func main() {
	input := flag.String("input", "lcl_modelo_go.csv.gz", "dataset de modelo (.csv o .csv.gz)")
	mode := flag.String("mode", "pronostico", "pronostico | walkforward | benchmark | tendencia")
	idCol := flag.String("id", "", "columna del hogar (por defecto se detecta: LCLid, household_id, ...)")
	outdir := flag.String("outdir", "resultados_series", "carpeta de salida")
	desde := flag.String("desde", "2012-07", "primer mes pronosticado en el walk-forward (AAAA-MM)")
	workers := flag.Int("workers", runtime.NumCPU(), "workers del walk-forward concurrente")
	orden := flag.String("orden", "desc", "orden de envío de los meses al pool: desc (LPT) | asc")
	reps := flag.Int("reps", 10, "repeticiones por configuración (benchmark)")
	wlist := flag.String("wlist", "1,2,4,8,16", "lista de workers (benchmark)")
	maxLag := flag.Int("maxlag", 14, "rezago máximo de la ACF de residuos")
	blockSize := flag.Int("block", 16384, "filas por bloque en la agregación concurrente (tendencia)")
	flag.Parse()

	if err := os.MkdirAll(*outdir, 0o755); err != nil {
		fatal(err)
	}
	fmt.Printf("CPU lógicas: %d | GOMAXPROCS: %d | Go %s\n", runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version())
	t0 := time.Now()
	d, err := loadAll(*input, *idCol)
	if err != nil {
		fatal(err)
	}
	n := [3]int{}
	for _, s := range d.Split {
		n[s]++
	}
	fmt.Printf("Filas: %d (train %d | validation %d | test %d) | hogares: %d | %s a %s | carga: %s\n",
		d.N, n[0], n[1], n[2], len(d.Hogars), fecha(d.Dia[0]).Format("2006-01-02"),
		fecha(d.Dia[d.N-1]).Format("2006-01-02"), time.Since(t0).Round(time.Millisecond))

	switch *mode {
	case "pronostico":
		modoPronostico(d, *outdir, *maxLag)
	case "walkforward":
		modoWalkforward(d, *outdir, *desde, *workers, *orden)
	case "benchmark":
		modoBenchmark(d, *outdir, *desde, *reps, parseInts(*wlist), *orden)
	case "tendencia":
		modoTendencia(d, *outdir, *reps, parseInts(*wlist), *blockSize)
	default:
		fatal(fmt.Errorf("modo no reconocido: %s", *mode))
	}
}
