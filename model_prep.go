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
	"sort"
	"strconv"
	"strings"
	"time"
)

type DailyRow struct {
	Date   time.Time
	Tariff string
	Raw    float64
}

type CapInfo struct {
	TrainDays int
	Q1        float64
	Q3        float64
	Cap       float64
	Rule      string
}

type SplitStats struct {
	Rows       int64
	CappedRows int64
	SumRaw     float64
	SumCapped  float64
	MaxRaw     float64
	MaxCapped  float64
}

func openCSVReader(path string) (io.ReadCloser, *csv.Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	var rc io.ReadCloser = f
	var reader io.Reader = bufio.NewReaderSize(f, 4*1024*1024)
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		rc = &multiCloser{Reader: gz, closers: []io.Closer{gz, f}}
		reader = gz
	}
	r := csv.NewReader(bufio.NewReaderSize(reader, 4*1024*1024))
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	return rc, r, nil
}

type multiCloser struct {
	io.Reader
	closers []io.Closer
}

func (m *multiCloser) Close() error {
	var first error
	for _, c := range m.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func createCSVWriter(path string) (io.WriteCloser, *csv.Writer, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	var wc io.WriteCloser = f
	var writer io.Writer = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz := gzip.NewWriter(f)
		wc = &writerCloser{gzipWriter: gz, file: f}
		writer = gz
	}
	w := csv.NewWriter(writer)
	return wc, w, nil
}

type writerCloser struct {
	gzipWriter *gzip.Writer
	file       *os.File
}

func (w *writerCloser) Write(p []byte) (int, error) { return w.gzipWriter.Write(p) }
func (w *writerCloser) Close() error {
	if err := w.gzipWriter.Close(); err != nil {
		w.file.Close()
		return err
	}
	return w.file.Close()
}

func indexHeader(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	return m
}

func field(row []string, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func quantileCont(sortedVals []float64, q float64) float64 {
	if len(sortedVals) == 0 {
		return math.NaN()
	}
	if len(sortedVals) == 1 {
		return sortedVals[0]
	}
	pos := q * float64(len(sortedVals)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sortedVals[lo]
	}
	frac := pos - float64(lo)
	return sortedVals[lo]*(1-frac) + sortedVals[hi]*frac
}

func splitForDate(d time.Time) string {
	trainEnd := time.Date(2013, 10, 1, 0, 0, 0, 0, time.UTC)
	validationEnd := time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC)
	if d.Before(trainEnd) {
		return "train"
	}
	if d.Before(validationEnd) {
		return "validation"
	}
	return "test"
}

func capped(x, cap float64) float64 {
	if x < 0 {
		return 0
	}
	if x > cap {
		return cap
	}
	return x
}

func meanStdSample(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return math.NaN(), math.NaN()
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	if len(xs) == 1 {
		return mean, math.NaN()
	}
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return mean, math.Sqrt(ss / float64(len(xs)-1))
}

func main() {
	inPath := flag.String("in", "lcl_diario_go.csv", "CSV diario generado por clean_daily.go")
	outPath := flag.String("out", "lcl_modelo_go.csv.gz", "dataset de modelo (.csv o .csv.gz)")
	auditPath := flag.String("audit", "audit_model_prep_go.csv", "auditoría de salida")
	flag.Parse()

	rc, r, err := openCSVReader(*inPath)
	if err != nil {
		panic(err)
	}
	defer rc.Close()

	header, err := r.Read()
	if err != nil {
		panic(err)
	}
	idx := indexHeader(header)
	required := []string{"household_id", "tariff", "reading_date", "readings_count", "daily_energy_kwh", "has_conflict"}
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			panic("falta columna requerida: " + c)
		}
	}

	byHousehold := make(map[string][]DailyRow, 6000)
	var inputRows, completeRows int64
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		inputRows++
		if field(row, idx, "has_conflict") == "true" {
			continue
		}
		count, err := strconv.Atoi(field(row, idx, "readings_count"))
		if err != nil || count != 48 {
			continue
		}
		household := field(row, idx, "household_id")
		tariff := field(row, idx, "tariff")
		if household == "" || (tariff != "Std" && tariff != "ToU") {
			continue
		}
		date, err := time.Parse("2006-01-02", field(row, idx, "reading_date"))
		if err != nil {
			continue
		}
		raw, err := strconv.ParseFloat(field(row, idx, "daily_energy_kwh"), 64)
		if err != nil || raw < 0 || math.IsNaN(raw) || math.IsInf(raw, 0) {
			continue
		}
		byHousehold[household] = append(byHousehold[household], DailyRow{Date: date, Tariff: tariff, Raw: raw})
		completeRows++
	}

	// Orden temporal por hogar y construcción del conjunto TRAIN.
	globalTrain := make([]float64, 0, completeRows*2/3)
	for h := range byHousehold {
		rows := byHousehold[h]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Date.Before(rows[j].Date) })
		byHousehold[h] = rows
		for _, row := range rows {
			if splitForDate(row.Date) == "train" {
				globalTrain = append(globalTrain, row.Raw)
			}
		}
	}
	if len(globalTrain) == 0 {
		panic("no hay datos de entrenamiento")
	}
	sort.Float64s(globalTrain)
	q1g := quantileCont(globalTrain, 0.25)
	q3g := quantileCont(globalTrain, 0.75)
	globalCap := q3g + 3.0*(q3g-q1g)

	caps := make(map[string]CapInfo, len(byHousehold))
	for h, rows := range byHousehold {
		trainVals := make([]float64, 0, len(rows))
		for _, row := range rows {
			if splitForDate(row.Date) == "train" {
				trainVals = append(trainVals, row.Raw)
			}
		}
		sort.Float64s(trainVals)
		info := CapInfo{TrainDays: len(trainVals), Q1: math.NaN(), Q3: math.NaN(), Cap: globalCap, Rule: "global_fallback"}
		if len(trainVals) > 0 {
			info.Q1 = quantileCont(trainVals, 0.25)
			info.Q3 = quantileCont(trainVals, 0.75)
		}
		if len(trainVals) >= 30 && info.Q3 > info.Q1 {
			householdCap := info.Q3 + 3.0*(info.Q3-info.Q1)
			if householdCap < globalCap {
				info.Cap = globalCap
				info.Rule = "global_floor"
			} else {
				info.Cap = householdCap
				info.Rule = "household_cap"
			}
		}
		caps[h] = info
	}

	wc, w, err := createCSVWriter(*outPath)
	if err != nil {
		panic(err)
	}
	defer wc.Close()

	outHeader := []string{
		"household_id", "reading_date", "tariff_tou", "is_weekend",
		"dow_sin", "dow_cos", "month_sin", "month_cos", "day_index",
		"lag_1_kwh", "lag_7_kwh", "rolling_mean_7_kwh", "rolling_std_7_kwh",
		"final_upper_cap", "target_was_capped", "dataset_split",
		"target_daily_kwh_raw", "target_daily_kwh_capped",
	}
	if err := w.Write(outHeader); err != nil {
		panic(err)
	}

	origin := time.Date(2011, 1, 1, 0, 0, 0, 0, time.UTC)
	stats := map[string]*SplitStats{"train": {}, "validation": {}, "test": {}}
	var modelRows int64

	houses := make([]string, 0, len(byHousehold))
	for h := range byHousehold {
		houses = append(houses, h)
	}
	sort.Strings(houses)

	for _, h := range houses {
		rows := byHousehold[h]
		capValue := caps[h].Cap
		cappedValues := make([]float64, len(rows))
		for i, row := range rows {
			cappedValues[i] = capped(row.Raw, capValue)
		}
		for i := 7; i < len(rows); i++ {
			// Equivalente a las condiciones del notebook previo:
			// debe existir el día anterior y el séptimo día anterior.
			if int(rows[i].Date.Sub(rows[i-1].Date).Hours()/24) != 1 {
				continue
			}
			if int(rows[i].Date.Sub(rows[i-7].Date).Hours()/24) != 7 {
				continue
			}
			prev7 := cappedValues[i-7 : i]
			rollMean, rollStd := meanStdSample(prev7)
			if math.IsNaN(rollStd) {
				continue
			}

			row := rows[i]
			dow := float64(row.Date.Weekday()) // Sunday=0 ... Saturday=6, igual que DuckDB DOW.
			month0 := float64(int(row.Date.Month()) - 1)
			weekend := 0
			if row.Date.Weekday() == time.Saturday || row.Date.Weekday() == time.Sunday {
				weekend = 1
			}
			tariffTOU := 0
			if row.Tariff == "ToU" {
				tariffTOU = 1
			}
			dayIndex := int(row.Date.Sub(origin).Hours() / 24)
			split := splitForDate(row.Date)
			wasCapped := 0
			if row.Raw > capValue {
				wasCapped = 1
			}
			targetCapped := cappedValues[i]

			record := []string{
				h,
				row.Date.Format("2006-01-02"),
				strconv.Itoa(tariffTOU),
				strconv.Itoa(weekend),
				strconv.FormatFloat(math.Sin(2*math.Pi*dow/7.0), 'g', -1, 64),
				strconv.FormatFloat(math.Cos(2*math.Pi*dow/7.0), 'g', -1, 64),
				strconv.FormatFloat(math.Sin(2*math.Pi*month0/12.0), 'g', -1, 64),
				strconv.FormatFloat(math.Cos(2*math.Pi*month0/12.0), 'g', -1, 64),
				strconv.Itoa(dayIndex),
				strconv.FormatFloat(cappedValues[i-1], 'g', -1, 64),
				strconv.FormatFloat(cappedValues[i-7], 'g', -1, 64),
				strconv.FormatFloat(rollMean, 'g', -1, 64),
				strconv.FormatFloat(rollStd, 'g', -1, 64),
				strconv.FormatFloat(capValue, 'g', -1, 64),
				strconv.Itoa(wasCapped),
				split,
				strconv.FormatFloat(row.Raw, 'g', -1, 64),
				strconv.FormatFloat(targetCapped, 'g', -1, 64),
			}
			if err := w.Write(record); err != nil {
				panic(err)
			}
			modelRows++
			s := stats[split]
			s.Rows++
			s.CappedRows += int64(wasCapped)
			s.SumRaw += row.Raw
			s.SumCapped += targetCapped
			if row.Raw > s.MaxRaw {
				s.MaxRaw = row.Raw
			}
			if targetCapped > s.MaxCapped {
				s.MaxCapped = targetCapped
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		panic(err)
	}
	if err := wc.Close(); err != nil {
		panic(err)
	}

	// Auditoría compacta y reproducible.
	af, err := os.Create(*auditPath)
	if err != nil {
		panic(err)
	}
	aw := csv.NewWriter(af)
	_ = aw.Write([]string{"section", "metric", "value"})
	_ = aw.Write([]string{"global", "daily_input_rows", strconv.FormatInt(inputRows, 10)})
	_ = aw.Write([]string{"global", "complete_daily_rows", strconv.FormatInt(completeRows, 10)})
	_ = aw.Write([]string{"global", "households", strconv.Itoa(len(byHousehold))})
	_ = aw.Write([]string{"global", "model_rows", strconv.FormatInt(modelRows, 10)})
	_ = aw.Write([]string{"global", "train_q1", strconv.FormatFloat(q1g, 'g', -1, 64)})
	_ = aw.Write([]string{"global", "train_q3", strconv.FormatFloat(q3g, 'g', -1, 64)})
	_ = aw.Write([]string{"global", "global_upper_cap", strconv.FormatFloat(globalCap, 'g', -1, 64)})
	for _, split := range []string{"train", "validation", "test"} {
		s := stats[split]
		_ = aw.Write([]string{split, "rows", strconv.FormatInt(s.Rows, 10)})
		_ = aw.Write([]string{split, "capped_rows", strconv.FormatInt(s.CappedRows, 10)})
		if s.Rows > 0 {
			_ = aw.Write([]string{split, "capped_pct", strconv.FormatFloat(100*float64(s.CappedRows)/float64(s.Rows), 'f', 4, 64)})
			_ = aw.Write([]string{split, "mean_raw", strconv.FormatFloat(s.SumRaw/float64(s.Rows), 'g', -1, 64)})
			_ = aw.Write([]string{split, "mean_capped", strconv.FormatFloat(s.SumCapped/float64(s.Rows), 'g', -1, 64)})
			_ = aw.Write([]string{split, "max_raw", strconv.FormatFloat(s.MaxRaw, 'g', -1, 64)})
			_ = aw.Write([]string{split, "max_capped", strconv.FormatFloat(s.MaxCapped, 'g', -1, 64)})
		}
	}
	aw.Flush()
	if err := aw.Error(); err != nil {
		panic(err)
	}
	if err := af.Close(); err != nil {
		panic(err)
	}

	fmt.Println("=== MODEL PREP GO ===")
	fmt.Printf("Hogares: %d\n", len(byHousehold))
	fmt.Printf("Días completos usados: %d\n", completeRows)
	fmt.Printf("Límite global 3xIQR (solo TRAIN): %.6f kWh\n", globalCap)
	fmt.Printf("Filas finales del modelo: %d\n", modelRows)
	fmt.Printf("Salida: %s\nAuditoría: %s\n", *outPath, *auditPath)
}
