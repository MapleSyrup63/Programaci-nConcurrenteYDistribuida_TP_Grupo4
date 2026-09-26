package main

import (
	"archive/zip"
	"bufio"
	"container/heap"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Stats struct {
	RawRows               int64
	ValidRows             int64
	MissingHousehold      int64
	InvalidTariff         int64
	MissingDateTime       int64
	DateTimeParseErrors   int64
	MissingEnergy         int64
	EnergyParseErrors     int64
	NegativeEnergy        int64
	IrregularTime         int64
	DuplicateRows         int64
	ConflictingDuplicates int64
	ConflictingTariffRows int64
	DaysTotal             int64
	CompleteDays          int64
	IncompleteDays        int64
	ConflictDays          int64
	CrossFileMergeDays    int64
	CrossFileOverlapDays  int64
}

func (s *Stats) Add(o Stats) {
	s.RawRows += o.RawRows
	s.ValidRows += o.ValidRows
	s.MissingHousehold += o.MissingHousehold
	s.InvalidTariff += o.InvalidTariff
	s.MissingDateTime += o.MissingDateTime
	s.DateTimeParseErrors += o.DateTimeParseErrors
	s.MissingEnergy += o.MissingEnergy
	s.EnergyParseErrors += o.EnergyParseErrors
	s.NegativeEnergy += o.NegativeEnergy
	s.IrregularTime += o.IrregularTime
	s.DuplicateRows += o.DuplicateRows
	s.ConflictingDuplicates += o.ConflictingDuplicates
	s.ConflictingTariffRows += o.ConflictingTariffRows
}

type DayAgg struct {
	Household string
	Tariff    string
	Date      string
	Seen      uint64
	Energy    [48]float64
	Conflict  bool
}

type FileResult struct {
	Source   string
	TempPath string
	Stats    Stats
	Err      error
}

func cleanText(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
	switch strings.ToLower(s) {
	case "", "null", "nan", "na", "n/a":
		return ""
	default:
		return s
	}
}

func parseDateTime(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	var lastErr error
	for _, layout := range layouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

func headerIndex(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[cleanText(h)] = i
	}
	return m
}

func requireColumns(idx map[string]int, names ...string) error {
	for _, name := range names {
		if _, ok := idx[name]; !ok {
			return fmt.Errorf("falta columna requerida %q", name)
		}
	}
	return nil
}

func safeField(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return cleanText(row[i])
}

// processEntry limpia un CSV del ZIP y genera agregados parciales por hogar-día.
// Importante: NO presupone que un hogar pertenezca a un único fragmento.
func processEntry(zf *zip.File, tempDir string) FileResult {
	result := FileResult{Source: zf.Name}

	rc, err := zf.Open()
	if err != nil {
		result.Err = err
		return result
	}
	defer rc.Close()

	r := csv.NewReader(bufio.NewReaderSize(rc, 4*1024*1024))
	r.FieldsPerRecord = -1
	r.ReuseRecord = true

	header, err := r.Read()
	if err != nil {
		result.Err = fmt.Errorf("%s: no se pudo leer encabezado: %w", zf.Name, err)
		return result
	}
	idx := headerIndex(header)
	if err := requireColumns(idx, "LCLid", "stdorToU", "DateTime", "KWH/hh (per half hour)"); err != nil {
		result.Err = fmt.Errorf("%s: %w; encabezado=%v", zf.Name, err, header)
		return result
	}

	days := make(map[string]*DayAgg, 30000)

	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Err = fmt.Errorf("%s: error CSV: %w", zf.Name, err)
			return result
		}
		result.Stats.RawRows++

		household := safeField(row, idx["LCLid"])
		if household == "" {
			result.Stats.MissingHousehold++
			continue
		}

		tariff := safeField(row, idx["stdorToU"])
		if tariff != "Std" && tariff != "ToU" {
			result.Stats.InvalidTariff++
			continue
		}

		dtText := safeField(row, idx["DateTime"])
		if dtText == "" {
			result.Stats.MissingDateTime++
			continue
		}
		dt, err := parseDateTime(dtText)
		if err != nil {
			result.Stats.DateTimeParseErrors++
			continue
		}
		if (dt.Minute() != 0 && dt.Minute() != 30) || dt.Second() != 0 {
			result.Stats.IrregularTime++
			continue
		}

		energyText := safeField(row, idx["KWH/hh (per half hour)"])
		if energyText == "" {
			result.Stats.MissingEnergy++
			continue
		}
		energy, err := strconv.ParseFloat(energyText, 64)
		if err != nil || math.IsNaN(energy) || math.IsInf(energy, 0) {
			result.Stats.EnergyParseErrors++
			continue
		}
		if energy < 0 {
			result.Stats.NegativeEnergy++
			continue
		}

		result.Stats.ValidRows++
		date := dt.Format("2006-01-02")
		key := household + "\x1f" + date
		agg := days[key]
		if agg == nil {
			agg = &DayAgg{Household: household, Tariff: tariff, Date: date}
			days[key] = agg
		}
		if agg.Tariff != tariff {
			agg.Conflict = true
			result.Stats.ConflictingTariffRows++
		}

		slot := dt.Hour()*2 + dt.Minute()/30
		bit := uint64(1) << uint(slot)
		if agg.Seen&bit != 0 {
			result.Stats.DuplicateRows++
			if math.Abs(agg.Energy[slot]-energy) > 1e-12 {
				agg.Conflict = true
				result.Stats.ConflictingDuplicates++
			}
			continue
		}
		agg.Seen |= bit
		agg.Energy[slot] = energy
	}

	base := filepath.Base(zf.Name)
	tmp, err := os.CreateTemp(tempDir, strings.TrimSuffix(base, ".csv")+"_*.part.csv")
	if err != nil {
		result.Err = err
		return result
	}
	result.TempPath = tmp.Name()
	bw := bufio.NewWriterSize(tmp, 2*1024*1024)
	w := csv.NewWriter(bw)

	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		agg := days[k]
		var sum float64
		zero := 0
		for i := 0; i < 48; i++ {
			bit := uint64(1) << uint(i)
			if agg.Seen&bit == 0 {
				continue
			}
			sum += agg.Energy[i]
			if agg.Energy[i] == 0 {
				zero++
			}
		}
		record := []string{
			agg.Household,
			agg.Tariff,
			agg.Date,
			strconv.FormatUint(agg.Seen, 16),
			strconv.FormatFloat(sum, 'g', -1, 64),
			strconv.Itoa(zero),
			strconv.FormatBool(agg.Conflict),
			zf.Name,
		}
		if err := w.Write(record); err != nil {
			result.Err = err
			break
		}
	}
	w.Flush()
	if err := w.Error(); err != nil && result.Err == nil {
		result.Err = err
	}
	if err := bw.Flush(); err != nil && result.Err == nil {
		result.Err = err
	}
	if err := tmp.Close(); err != nil && result.Err == nil {
		result.Err = err
	}
	return result
}

type PartRow struct {
	Household string
	Tariff    string
	Date      string
	Seen      uint64
	Sum       float64
	Zero      int
	Conflict  bool
	Source    string
}

func (p PartRow) Key() string { return p.Household + "\x1f" + p.Date }

func parsePartRow(rec []string) (PartRow, error) {
	if len(rec) < 8 {
		return PartRow{}, fmt.Errorf("registro parcial con %d columnas", len(rec))
	}
	mask, err := strconv.ParseUint(rec[3], 16, 64)
	if err != nil {
		return PartRow{}, err
	}
	sum, err := strconv.ParseFloat(rec[4], 64)
	if err != nil {
		return PartRow{}, err
	}
	zero, err := strconv.Atoi(rec[5])
	if err != nil {
		return PartRow{}, err
	}
	conflict, err := strconv.ParseBool(rec[6])
	if err != nil {
		return PartRow{}, err
	}
	return PartRow{rec[0], rec[1], rec[2], mask, sum, zero, conflict, rec[7]}, nil
}

type partCursor struct {
	idx    int
	file   *os.File
	reader *csv.Reader
	row    PartRow
}

type cursorHeap []*partCursor

func (h cursorHeap) Len() int { return len(h) }
func (h cursorHeap) Less(i, j int) bool {
	if h[i].row.Key() == h[j].row.Key() {
		return h[i].idx < h[j].idx
	}
	return h[i].row.Key() < h[j].row.Key()
}
func (h cursorHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *cursorHeap) Push(x any)   { *h = append(*h, x.(*partCursor)) }
func (h *cursorHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func advanceCursor(c *partCursor) (bool, error) {
	rec, err := c.reader.Read()
	if err == io.EOF {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	row, err := parsePartRow(rec)
	if err != nil {
		return false, err
	}
	c.row = row
	return true, nil
}

// mergeParts hace un k-way merge de los archivos parciales ya ordenados.
// Así se pueden unir días partidos entre CSVs sin cargar millones de días en RAM.
func mergeParts(parts []FileResult, outPath string, stats *Stats) (int, error) {
	if dir := filepath.Dir(outPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	out, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	bw := bufio.NewWriterSize(out, 4*1024*1024)
	cw := csv.NewWriter(bw)
	if err := cw.Write([]string{"household_id", "tariff", "reading_date", "readings_count", "daily_energy_kwh", "zero_readings", "has_conflict", "source_file"}); err != nil {
		return 0, err
	}

	h := &cursorHeap{}
	heap.Init(h)
	cursors := make([]*partCursor, 0, len(parts))
	for i, p := range parts {
		f, err := os.Open(p.TempPath)
		if err != nil {
			return 0, err
		}
		c := &partCursor{idx: i, file: f, reader: csv.NewReader(bufio.NewReaderSize(f, 1*1024*1024))}
		c.reader.FieldsPerRecord = -1
		ok, err := advanceCursor(c)
		if err != nil {
			f.Close()
			return 0, err
		}
		if ok {
			heap.Push(h, c)
			cursors = append(cursors, c)
		} else {
			f.Close()
		}
	}
	defer func() {
		for _, c := range cursors {
			_ = c.file.Close()
		}
	}()

	uniqueHouseholds := 0
	lastHousehold := ""

	for h.Len() > 0 {
		c := heap.Pop(h).(*partCursor)
		agg := c.row
		key := agg.Key()
		sources := []string{agg.Source}
		sourceSet := map[string]struct{}{agg.Source: {}}
		mergedFiles := 1

		ok, err := advanceCursor(c)
		if err != nil {
			return 0, err
		}
		if ok {
			heap.Push(h, c)
		}

		// Consumir todas las filas parciales con la misma clave hogar-fecha.
		for h.Len() > 0 && (*h)[0].row.Key() == key {
			c2 := heap.Pop(h).(*partCursor)
			r2 := c2.row
			mergedFiles++
			if _, exists := sourceSet[r2.Source]; !exists {
				sourceSet[r2.Source] = struct{}{}
				sources = append(sources, r2.Source)
			}
			if agg.Tariff != r2.Tariff {
				agg.Conflict = true
				stats.ConflictingTariffRows++
			}
			overlap := agg.Seen & r2.Seen
			if overlap != 0 {
				// No tenemos los 48 valores individuales en el archivo parcial;
				// por seguridad, un solapamiento entre fragmentos se marca conflictivo.
				agg.Conflict = true
				stats.CrossFileOverlapDays++
			}
			agg.Seen |= r2.Seen
			agg.Sum += r2.Sum
			agg.Zero += r2.Zero
			agg.Conflict = agg.Conflict || r2.Conflict

			ok, err := advanceCursor(c2)
			if err != nil {
				return 0, err
			}
			if ok {
				heap.Push(h, c2)
			}
		}

		if mergedFiles > 1 {
			stats.CrossFileMergeDays++
		}
		sort.Strings(sources)
		count := bits.OnesCount64(agg.Seen)
		stats.DaysTotal++
		if agg.Conflict {
			stats.ConflictDays++
		}
		if count == 48 && !agg.Conflict {
			stats.CompleteDays++
		} else {
			stats.IncompleteDays++
		}

		if agg.Household != lastHousehold {
			uniqueHouseholds++
			lastHousehold = agg.Household
		}

		rec := []string{
			agg.Household,
			agg.Tariff,
			agg.Date,
			strconv.Itoa(count),
			strconv.FormatFloat(agg.Sum, 'g', -1, 64),
			strconv.Itoa(agg.Zero),
			strconv.FormatBool(agg.Conflict),
			strings.Join(sources, ";"),
		}
		if err := cw.Write(rec); err != nil {
			return 0, err
		}
	}

	cw.Flush()
	if err := cw.Error(); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return uniqueHouseholds, nil
}

func writeAudit(path string, stats Stats, sourceFiles, households int) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"metric", "value"})
	metrics := []struct {
		name  string
		value int64
	}{
		{"source_csv_files", int64(sourceFiles)},
		{"unique_households", int64(households)},
		{"raw_rows", stats.RawRows},
		{"valid_rows", stats.ValidRows},
		{"missing_household", stats.MissingHousehold},
		{"invalid_tariff", stats.InvalidTariff},
		{"missing_datetime", stats.MissingDateTime},
		{"datetime_parse_errors", stats.DateTimeParseErrors},
		{"missing_energy", stats.MissingEnergy},
		{"energy_parse_errors", stats.EnergyParseErrors},
		{"negative_energy", stats.NegativeEnergy},
		{"irregular_time", stats.IrregularTime},
		{"duplicate_rows", stats.DuplicateRows},
		{"conflicting_duplicates", stats.ConflictingDuplicates},
		{"conflicting_tariff_rows", stats.ConflictingTariffRows},
		{"household_days", stats.DaysTotal},
		{"complete_household_days", stats.CompleteDays},
		{"incomplete_or_conflict_days", stats.IncompleteDays},
		{"conflict_days", stats.ConflictDays},
		{"cross_file_merge_days", stats.CrossFileMergeDays},
		{"cross_file_overlap_days", stats.CrossFileOverlapDays},
	}
	for _, m := range metrics {
		if err := w.Write([]string{m.name, strconv.FormatInt(m.value, 10)}); err != nil {
			return err
		}
	}
	return w.Error()
}

func main() {
	zipPath := flag.String("zip", "", "ruta al ZIP Smart Meters in London")
	outPath := flag.String("out", "lcl_diario_go.csv", "CSV diario de salida")
	auditPath := flag.String("audit", "audit_cleaning_go.csv", "CSV de auditoría")
	workers := flag.Int("workers", min(4, runtime.NumCPU()), "número de workers concurrentes")
	flag.Parse()

	if *zipPath == "" {
		fmt.Fprintln(os.Stderr, "ERROR: usa -zip /ruta/dataset.zip")
		os.Exit(2)
	}
	if *workers < 1 {
		*workers = 1
	}

	zr, err := zip.OpenReader(*zipPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR abriendo ZIP:", err)
		os.Exit(1)
	}
	defer zr.Close()

	entries := make([]*zip.File, 0, 200)
	for _, zf := range zr.File {
		name := strings.ToLower(zf.Name)
		if strings.Contains(zf.Name, "LCL-June2015v2_") && strings.HasSuffix(name, ".csv") {
			entries = append(entries, zf)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: no se encontraron fragmentos LCL-June2015v2_*.csv dentro del ZIP")
		fmt.Fprintln(os.Stderr, "Sugerencia: inspecciona los nombres con Python zipfile antes de ejecutar la limpieza.")
		os.Exit(1)
	}

	fmt.Printf("Fragmentos detectados: %d | workers: %d\n", len(entries), min(*workers, len(entries)))

	tempDir, err := os.MkdirTemp("", "lcl_go_parts_*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR creando temporales:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	jobs := make(chan *zip.File)
	results := make(chan FileResult, len(entries))
	var wg sync.WaitGroup
	for i := 0; i < min(*workers, len(entries)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for zf := range jobs {
				results <- processEntry(zf, tempDir)
			}
		}()
	}
	go func() {
		for _, zf := range entries {
			jobs <- zf
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	all := make([]FileResult, 0, len(entries))
	var total Stats
	done := 0
	for res := range results {
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "ERROR en %s: %v\n", res.Source, res.Err)
			os.Exit(1)
		}
		done++
		all = append(all, res)
		total.Add(res.Stats)
		fmt.Printf("OK %3d/%d %-32s raw=%d\n", done, len(entries), filepath.Base(res.Source), res.Stats.RawRows)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Source < all[j].Source })
	fmt.Println("Uniendo agregados hogar-día entre fragmentos...")
	households, err := mergeParts(all, *outPath, &total)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR durante merge final:", err)
		os.Exit(1)
	}

	if err := writeAudit(*auditPath, total, len(entries), households); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR escribiendo auditoría:", err)
		os.Exit(1)
	}

	fmt.Println("\n=== RESUMEN ===")
	fmt.Printf("CSV fuente: %d\n", len(entries))
	fmt.Printf("Hogares únicos: %d\n", households)
	fmt.Printf("Filas crudas leídas: %d\n", total.RawRows)
	fmt.Printf("Filas válidas: %d\n", total.ValidRows)
	fmt.Printf("Hogar-día generados: %d\n", total.DaysTotal)
	fmt.Printf("Hogar-día completos: %d\n", total.CompleteDays)
	fmt.Printf("Días unidos desde >1 fragmento: %d\n", total.CrossFileMergeDays)
	fmt.Printf("Días con solapamiento entre fragmentos: %d\n", total.CrossFileOverlapDays)
	fmt.Printf("Salida: %s\nAuditoría: %s\n", *outPath, *auditPath)
}
