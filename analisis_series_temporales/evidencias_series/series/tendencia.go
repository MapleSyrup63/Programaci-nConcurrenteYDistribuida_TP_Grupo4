// tendencia.go — Análisis de tendencia y estacionalidad del consumo diario (modo -mode tendencia).
//
//  1. Agrega las 3.28 millones de filas hogar-día en una serie diaria (consumo medio por
//     hogar). La agregación es una reducción: se implementa en versión secuencial y en
//     versión concurrente (Worker Pool + sync.Mutex + sync.WaitGroup, el mismo protocolo
//     verificado en Spin) y se mide su speedup.
//  2. Descompone la serie (descomposición clásica multiplicativa):
//     tendencia     medias móviles centradas de 7, 30 y 365 días, y recta de mínimos cuadrados
//     estacionalidad índice semanal (día de la semana) e índice mensual
//     comparación   mismo mes en 2012 y en 2013 (variación interanual)
//  3. Repite el análisis sobre un PANEL BALANCEADO (hogares presentes casi todos los días de
//     2012 y 2013), porque el piloto incorporó hogares de forma gradual y la media de todos
//     los hogares mezcla cambios de consumo con cambios en la composición de la muestra.
package main

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Agg acumula suma y conteo de consumo por día (índice = Dia - d0).
type Agg struct {
	Sum []float64
	Cnt []int32
}

func nuevaAgg(nd int) Agg { return Agg{make([]float64, nd), make([]int32, nd)} }

// aggRange es el núcleo común de ambas versiones. sel == nil considera todos los hogares.
func aggRange(d *Data, lo, hi int, d0 int32, sel []bool, a *Agg) {
	for r := lo; r < hi; r++ {
		if sel != nil && !sel[d.Hogar[r]] {
			continue
		}
		k := d.Dia[r] - d0
		a.Sum[k] += d.Y[r]
		a.Cnt[k]++
	}
}

func aggSecuencial(d *Data, d0 int32, nd int, sel []bool) Agg {
	a := nuevaAgg(nd)
	aggRange(d, 0, d.N, d0, sel, &a)
	return a
}

// aggConcurrente: Worker Pool. Cada worker acumula en arreglos LOCALES por día y,
// al terminar, fusiona su parcial en el acumulador global dentro de la sección crítica.
func aggConcurrente(d *Data, d0 int32, nd int, sel []bool, workers, blockSize int) Agg {
	global := nuevaAgg(nd)
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan block, workers*2)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := nuevaAgg(nd)
			for b := range jobs {
				aggRange(d, b.lo, b.hi, d0, sel, &local)
			}
			mu.Lock() // ---- sección crítica: una vez por worker ----
			for k := 0; k < nd; k++ {
				global.Sum[k] += local.Sum[k]
				global.Cnt[k] += local.Cnt[k]
			}
			mu.Unlock()
		}()
	}
	for lo := 0; lo < d.N; lo += blockSize {
		hi := lo + blockSize
		if hi > d.N {
			hi = d.N
		}
		jobs <- block{lo, hi}
	}
	close(jobs)
	wg.Wait()
	return global
}

type block struct{ lo, hi int }

// mediaMovil centrada de ventana w; NaN si la ventana sale del rango o tiene días sin dato.
func mediaMovil(x []float64, w int) []float64 {
	out := make([]float64, len(x))
	izq := w / 2
	der := w - 1 - izq
	for i := range x {
		out[i] = math.NaN()
		if i-izq < 0 || i+der >= len(x) {
			continue
		}
		var s float64
		ok := true
		for j := i - izq; j <= i+der; j++ {
			if math.IsNaN(x[j]) {
				ok = false
				break
			}
			s += x[j]
		}
		if ok {
			out[i] = s / float64(w)
		}
	}
	return out
}

func mediaDiaria(a Agg) []float64 {
	m := make([]float64, len(a.Sum))
	for k := range m {
		if a.Cnt[k] > 0 {
			m[k] = a.Sum[k] / float64(a.Cnt[k])
		} else {
			m[k] = math.NaN()
		}
	}
	return m
}

// recta ajusta y = a + b·t por mínimos cuadrados sobre los índices [lo, hi) con dato.
func recta(y []float64, lo, hi int) (a, b, r2 float64) {
	var n, st, sy, stt, sty float64
	for t := lo; t < hi; t++ {
		if math.IsNaN(y[t]) {
			continue
		}
		ft := float64(t - lo)
		n++
		st += ft
		sy += y[t]
		stt += ft * ft
		sty += ft * y[t]
	}
	b = (n*sty - st*sy) / (n*stt - st*st)
	a = (sy - b*st) / n
	var sse, sst float64
	my := sy / n
	for t := lo; t < hi; t++ {
		if math.IsNaN(y[t]) {
			continue
		}
		e := y[t] - (a + b*float64(t-lo))
		sse += e * e
		sst += (y[t] - my) * (y[t] - my)
	}
	return a, b, 1 - sse/sst
}

func fNaN(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	return f4(v)
}

var diasSemana = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

func modoTendencia(d *Data, outdir string, reps int, wlist []int, blockSize int) {
	d0 := d.Dia[0]
	nd := int(d.Dia[d.N-1]-d0) + 1
	ini := diaDe(time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)) - d0 // ventana de 2 años completos
	fin := diaDe(time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC)) - d0
	if ini < 0 || int(fin) > nd {
		fatal(fmt.Errorf("el dataset no cubre completo 2012-2013"))
	}

	// ---------- 1. Tiempo de ejecución de la agregación: secuencial vs concurrente
	fmt.Printf("\n=== AGREGACIÓN DE LA SERIE DIARIA: %d filas -> %d días (%d repeticiones) ===\n", d.N, nd, reps)
	f, w := crearCSV(outdir, "tendencia_tiempos.csv", []string{"modo", "workers", "rep", "wall_ms", "cpu_ms"})
	configs := append([]int{0}, wlist...)
	var tSeq float64
	for _, nw := range configs {
		modo := "secuencial"
		if nw > 0 {
			modo = "concurrente"
		}
		ejecutar := func() Agg {
			if nw == 0 {
				return aggSecuencial(d, d0, nd, nil)
			}
			return aggConcurrente(d, d0, nd, nil, nw, blockSize)
		}
		ejecutar() // calentamiento
		walls := make([]float64, 0, reps)
		for r := 1; r <= reps; r++ {
			runtime.GC()
			c0, t0 := cpuTime(), time.Now()
			ejecutar()
			wall, cpu := time.Since(t0), cpuTime()-c0
			walls = append(walls, ms(wall))
			w.Write([]string{modo, strconv.Itoa(nw), strconv.Itoa(r), f4(ms(wall)), f4(ms(cpu))})
		}
		tm := trimmedMean(walls, 0.10)
		if nw == 0 {
			tSeq = tm
		}
		fmt.Printf("%-11s workers=%-3d media recortada 10%% = %8.2f ms | speedup %.2fx\n", modo, nw, tm, tSeq/tm)
	}
	cerrar(f, w)

	// La concurrencia no debe cambiar la serie (salvo redondeo por el orden de las sumas).
	seq := aggSecuencial(d, d0, nd, nil)
	con := aggConcurrente(d, d0, nd, nil, configs[len(configs)-1], blockSize)
	var difRel float64
	for k := 0; k < nd; k++ {
		if seq.Cnt[k] != con.Cnt[k] {
			fatal(fmt.Errorf("conteos distintos en el día %d", k))
		}
		if seq.Sum[k] != 0 {
			difRel = math.Max(difRel, math.Abs(seq.Sum[k]-con.Sum[k])/math.Abs(seq.Sum[k]))
		}
	}
	fmt.Printf("Conteos idénticos; diferencia relativa máxima de sumas: %.1e (orden de suma en punto flotante)\n", difRel)

	// ---------- 2. Panel balanceado: hogares con dato en >= 95 % de los días de 2012-2013
	diasHogar := make([]int, len(d.Hogars))
	for r := 0; r < d.N; r++ {
		if k := d.Dia[r] - d0; k >= ini && k < fin {
			diasHogar[d.Hogar[r]]++
		}
	}
	sel := make([]bool, len(d.Hogars))
	nPanel := 0
	for h, c := range diasHogar {
		if float64(c) >= 0.95*float64(fin-ini) {
			sel[h] = true
			nPanel++
		}
	}
	if nPanel == 0 {
		fatal(fmt.Errorf("ningún hogar cumple el criterio del panel balanceado"))
	}
	pan := aggConcurrente(d, d0, nd, sel, configs[len(configs)-1], blockSize)
	fmt.Printf("Panel balanceado: %d de %d hogares con dato en al menos 95 %% de los días de 2012-2013\n", nPanel, len(d.Hogars))

	// ---------- 2b. Variación intra-hogar: cada hogar comparado consigo mismo, mismo mes de 2012 y de 2013.
	// Usa a todos los hogares con al menos 20 días de dato en ese mes en ambos años, así que no depende
	// de quiénes entran o salen de la muestra (controla la composición sin reducirse al panel).
	type acum struct {
		s [2][12]float64
		c [2][12]int
	}
	porHogar := make([]acum, len(d.Hogars))
	for r := 0; r < d.N; r++ {
		if k := d.Dia[r] - d0; k >= ini && k < fin {
			t := fecha(d.Dia[r])
			a := &porHogar[d.Hogar[r]]
			a.s[t.Year()-2012][t.Month()-1] += d.Y[r]
			a.c[t.Year()-2012][t.Month()-1]++
		}
	}
	var s12, s13 float64
	var pares, hogaresIntra int
	ratios := make([]float64, 0)
	for h := range porHogar {
		a := &porHogar[h]
		usado := false
		for m := 0; m < 12; m++ {
			if a.c[0][m] >= 20 && a.c[1][m] >= 20 {
				m12, m13 := a.s[0][m]/float64(a.c[0][m]), a.s[1][m]/float64(a.c[1][m])
				s12 += m12
				s13 += m13
				pares++
				usado = true
				if m12 > 0 {
					ratios = append(ratios, m13/m12)
				}
			}
		}
		if usado {
			hogaresIntra++
		}
	}
	sort.Float64s(ratios)
	medianaIntra := math.NaN()
	if len(ratios) > 0 {
		medianaIntra = ratios[len(ratios)/2]
	}
	fi, wi := crearCSV(outdir, "tendencia_intrahogar.csv", []string{"hogares", "pares_hogar_mes", "var_agregada_pct", "var_mediana_pct"})
	wi.Write([]string{strconv.Itoa(hogaresIntra), strconv.Itoa(pares), f4(100 * (s13/s12 - 1)), f4(100 * (medianaIntra - 1))})
	cerrar(fi, wi)
	fmt.Printf("Intra-hogar: %d hogares, %d pares hogar-mes | variación 2013 vs 2012: agregada %+.2f %% | mediana %+.2f %%\n",
		hogaresIntra, pares, 100*(s13/s12-1), 100*(medianaIntra-1))

	series := []struct {
		nombre string
		m      []float64
		cnt    []int32
	}{{"todos", mediaDiaria(seq), seq.Cnt}, {"panel", mediaDiaria(pan), pan.Cnt}}

	// ---------- 3. Serie diaria con medias móviles
	ma := map[string][3][]float64{}
	for _, s := range series {
		ma[s.nombre] = [3][]float64{mediaMovil(s.m, 7), mediaMovil(s.m, 30), mediaMovil(s.m, 365)}
	}
	f, w = crearCSV(outdir, "tendencia_diaria.csv", []string{"fecha", "hogares", "media_kwh", "ma7", "ma30", "ma365",
		"hogares_panel", "media_panel", "ma7_panel", "ma30_panel", "ma365_panel"})
	for k := 0; k < nd; k++ {
		t, p := ma["todos"], ma["panel"]
		w.Write([]string{fecha(d0 + int32(k)).Format("2006-01-02"),
			strconv.Itoa(int(series[0].cnt[k])), fNaN(series[0].m[k]), fNaN(t[0][k]), fNaN(t[1][k]), fNaN(t[2][k]),
			strconv.Itoa(int(series[1].cnt[k])), fNaN(series[1].m[k]), fNaN(p[0][k]), fNaN(p[1][k]), fNaN(p[2][k])})
	}
	cerrar(f, w)

	// ---------- 4. Tendencia lineal, índices estacionales y comparación interanual (2012-2013)
	fr, wr := crearCSV(outdir, "tendencia_resumen.csv", []string{"serie", "hogares_promedio", "media_2012", "media_2013",
		"var_anual_pct", "pendiente_kwh_por_anio", "pendiente_pct_por_anio", "r2_recta", "ma365_inicio", "ma365_fin", "var_ma365_pct"})
	fs, ws := crearCSV(outdir, "indice_semanal.csv", []string{"serie", "dia", "indice"})
	fm, wm := crearCSV(outdir, "indice_mensual.csv", []string{"serie", "mes", "media_2012", "media_2013", "var_interanual_pct", "indice"})
	for _, s := range series {
		m := s.m
		// medias anuales y conteo promedio de hogares
		var sum [2]float64
		var cnt [2]int
		var hog float64
		for k := ini; k < fin; k++ {
			hog += float64(s.cnt[k])
			if math.IsNaN(m[k]) {
				continue
			}
			y := fecha(d0+k).Year() - 2012
			sum[y] += m[k]
			cnt[y]++
		}
		m12, m13 := sum[0]/float64(cnt[0]), sum[1]/float64(cnt[1])
		_, b, r2 := recta(m, int(ini), int(fin))
		bAnio := b * 365.25
		media := (sum[0] + sum[1]) / float64(cnt[0]+cnt[1])
		// tendencia de largo plazo: primera y última media móvil de 365 días disponibles en la ventana
		m365 := ma[s.nombre][2]
		a365, z365 := math.NaN(), math.NaN()
		for k := ini; k < fin; k++ {
			if !math.IsNaN(m365[k]) {
				if math.IsNaN(a365) {
					a365 = m365[k]
				}
				z365 = m365[k]
			}
		}
		wr.Write([]string{s.nombre, f4(hog / float64(fin-ini)), f4(m12), f4(m13), f4(100 * (m13/m12 - 1)),
			f4(bAnio), f4(100 * bAnio / media), f4(r2), fNaN(a365), fNaN(z365), fNaN(100 * (z365/a365 - 1))})
		fmt.Printf("\n[%s] media 2012 = %.3f | media 2013 = %.3f kWh/día (%+.2f %%) | recta: %+.3f kWh/día por año (%+.2f %%/año, R2 %.3f)\n",
			s.nombre, m12, m13, 100*(m13/m12-1), bAnio, 100*bAnio/media, r2)
		if !math.IsNaN(a365) {
			fmt.Printf("[%s] media móvil de 365 días: %.3f -> %.3f kWh/día (%+.2f %%)\n", s.nombre, a365, z365, 100*(z365/a365-1))
		}

		// índice semanal (descomposición multiplicativa): promedio de m_t / MA7_t por día de la semana
		ma7 := ma[s.nombre][0]
		var rs [7]float64
		var rc [7]int
		for k := ini; k < fin; k++ {
			if math.IsNaN(m[k]) || math.IsNaN(ma7[k]) || ma7[k] == 0 {
				continue
			}
			dw := int(fecha(d0 + k).Weekday())
			rs[dw] += m[k] / ma7[k]
			rc[dw]++
		}
		var norm float64
		var idx [7]float64
		for i := range idx {
			idx[i] = rs[i] / float64(rc[i])
			norm += idx[i] / 7
		}
		fmt.Printf("[%s] índice semanal:", s.nombre)
		for _, i := range []int{1, 2, 3, 4, 5, 6, 0} { // de lunes a domingo
			ws.Write([]string{s.nombre, diasSemana[i], f4(idx[i] / norm)})
			fmt.Printf(" %s %.3f", string([]rune(diasSemana[i])[:3]), idx[i]/norm)
		}
		fmt.Println()

		// índice mensual y comparación interanual
		var ms_ [2][12]float64
		var mc [2][12]int
		for k := ini; k < fin; k++ {
			if math.IsNaN(m[k]) {
				continue
			}
			t := fecha(d0 + k)
			ms_[t.Year()-2012][t.Month()-1] += m[k]
			mc[t.Year()-2012][t.Month()-1]++
		}
		for mes := 0; mes < 12; mes++ {
			a, z := ms_[0][mes]/float64(mc[0][mes]), ms_[1][mes]/float64(mc[1][mes])
			ind := (ms_[0][mes] + ms_[1][mes]) / float64(mc[0][mes]+mc[1][mes]) / media
			wm.Write([]string{s.nombre, strconv.Itoa(mes + 1), f4(a), f4(z), f4(100 * (z/a - 1)), f4(ind)})
		}
	}
	cerrar(fr, wr)
	cerrar(fs, ws)
	cerrar(fm, wm)
}
