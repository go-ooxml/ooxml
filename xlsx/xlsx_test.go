// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package xlsx

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/go-ooxml/ooxml"
	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

var paris = time.FixedZone("CET", 3600)

// report is the kind of workbook the finance reports are: a header, typed
// rows, and the texts a formula-injection test needs.
func report(t testing.TB) *Workbook {
	t.Helper()
	wb := New()
	sh, err := wb.AddSheet("Costs")
	must(t, err)
	must(t, sh.SetHeader("Model", "Tokens", "Cost", "Share", "Day", "Billed", "Note"))
	must(t, sh.SetColumnWidth(6, 40))
	must(t, sh.SetColumnWidth(0, 28.5))
	day := time.Date(2026, 10, 26, 23, 30, 0, 0, paris)
	must(t, sh.AppendRow(String("opus"), Number(1234567), Number(1234.5).Format(Decimal(2)),
		Number(0.425).Format(Percent(1)), Date(day), Bool(true), String("=1+1")))
	must(t, sh.AppendRow(String("sonnet"), Number(-0.0), Number(1e21), Number(0.0001).Format(Percent(0)),
		Date(day).Format(DateTimeFormat), Bool(false), String("_x0041_ and \x01")))
	must(t, sh.AppendRow()) // an empty row stays a row
	must(t, sh.AppendRow(String("Total").Bold(), Cell{}, Number(1234.5).Format(Decimal(2)).Bold(),
		Cell{}, Cell{}, Cell{}, String("  spaced  ")))
	other, err := wb.AddSheet("Résumé 😀")
	must(t, err)
	must(t, other.AppendRow(String("opus"), String("@SUM(A1)"), String("+1"), String("-1")))
	return wb
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func cellText(c richdoc.Cell) string {
	var sb strings.Builder
	for _, in := range c.Inlines {
		sb.WriteString(in.(richdoc.Text).Value)
	}
	return sb.String()
}

// TestRoundTripThroughOurReader: what the parent package's reader sees. It
// proves the two agree — the weakest judge, which is why the LibreOffice and
// openpyxl judges exist too.
func TestRoundTripThroughOurReader(t *testing.T) {
	data, err := report(t).Write()
	must(t, err)
	doc, err := ooxml.ParseWorkbook(data, opc.Limits{})
	must(t, err)
	if len(doc.Blocks) != 4 {
		t.Fatalf("blocks: %#v", doc.Blocks)
	}
	if h := doc.Blocks[0].(richdoc.Heading); cellText(richdoc.Cell{Inlines: h.Inlines}) != "Costs" {
		t.Errorf("first sheet is %v", h)
	}
	if h := doc.Blocks[2].(richdoc.Heading); cellText(richdoc.Cell{Inlines: h.Inlines}) != "Résumé 😀" {
		t.Errorf("second sheet is %v", h)
	}
	rows := doc.Blocks[1].(richdoc.Table).Rows
	want := [][]string{
		{"Model", "Tokens", "Cost", "Share", "Day", "Billed", "Note"},
		// 2026-10-26 23:30 on its own wall clock: day 46321, 23.5/24 of it.
		{"opus", "1234567", "1234.5", "0.425", "46321.979166666664", "TRUE", "=1+1"},
		{"sonnet", "0", "1E+21", "0.0001", "46321.979166666664", "FALSE", "_x0041_ and \x01"},
		{"", "", "", "", "", "", ""},
		{"Total", "", "1234.5", "", "", "", "  spaced  "},
	}
	if len(rows) != len(want) {
		t.Fatalf("%d rows, want %d", len(rows), len(want))
	}
	for r := range want {
		for c := range want[r] {
			if got := cellText(rows[r][c]); got != want[r][c] {
				t.Errorf("row %d col %d = %q, want %q", r+1, c+1, got, want[r][c])
			}
		}
	}
	second := doc.Blocks[3].(richdoc.Table).Rows[0]
	for i, w := range []string{"opus", "@SUM(A1)", "+1", "-1"} {
		if got := cellText(second[i]); got != w {
			t.Errorf("second sheet col %d = %q, want %q", i+1, got, w)
		}
	}
}

// part opens one part of a written workbook.
func part(t *testing.T, data []byte, name string) string {
	t.Helper()
	p, err := opc.Open(data, opc.Limits{})
	must(t, err)
	b, err := p.Part(name)
	must(t, err)
	return string(b)
}

// TestSpelling asserts what a forgiving reader cannot see: the frozen pane,
// the widths, the styles, the formula-free cells, element order.
func TestSpelling(t *testing.T) {
	data, err := report(t).Write()
	must(t, err)
	sheet := part(t, data, "xl/worksheets/sheet1.xml")
	for _, want := range []string{
		`<dimension ref="A1:G5"/>`,
		`<sheetView workbookViewId="0" tabSelected="1"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/><selection pane="bottomLeft" activeCell="A2" sqref="A2"/></sheetView>`,
		`<cols><col min="1" max="1" width="28.5" customWidth="1"/><col min="7" max="7" width="40" customWidth="1"/></cols>`,
		`<row r="4"></row>`,
		`<c r="B3"><v>0</v></c>`,
		`<c r="C3"><v>1E+21</v></c>`,
		`<c r="F2" t="b"><v>1</v></c>`,
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet1 lacks %s", want)
		}
	}
	order := []string{"<dimension", "<sheetViews", "<sheetFormatPr", "<cols", "<sheetData"}
	at := -1
	for _, e := range order {
		i := strings.Index(sheet, e)
		if i <= at {
			t.Errorf("%s is out of CT_Worksheet's order", e)
		}
		at = i
	}
	// ⛔ No formula, anywhere.
	for _, name := range []string{"xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		if s := part(t, data, name); strings.Contains(s, "<f>") || strings.Contains(s, "<f ") {
			t.Errorf("%s holds a formula", name)
		}
	}
	if s := part(t, data, "xl/worksheets/sheet2.xml"); strings.Contains(s, "pane") || strings.Contains(s, "tabSelected") || strings.Contains(s, "<cols>") {
		t.Errorf("sheet2 has a pane, a selected tab or widths it was not given: %s", s)
	}
	styles := part(t, data, "xl/styles.xml")
	for _, want := range []string{
		`<numFmt numFmtId="164" formatCode="#,##0.00"/>`,
		`<numFmt numFmtId="165" formatCode="0.0%"/>`,
		`<numFmt numFmtId="166" formatCode="yyyy-mm-dd"/>`,
		`<numFmt numFmtId="167" formatCode="0%"/>`,
		`<numFmt numFmtId="168" formatCode="yyyy-mm-dd hh:mm:ss"/>`,
		`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>`,
		`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>`,
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" quotePrefix="1"/>`,
		`<xf numFmtId="164" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/>`,
	} {
		if !strings.Contains(styles, want) {
			t.Errorf("styles lack %s", want)
		}
	}
	sst := part(t, data, "xl/sharedStrings.xml")
	for _, want := range []string{
		`<t xml:space="preserve">_x005F_x0041_ and _x0001_</t>`,
		`<t xml:space="preserve">=1+1</t>`,
		`<t xml:space="preserve">  spaced  </t>`,
	} {
		if !strings.Contains(sst, want) {
			t.Errorf("shared strings lack %s", want)
		}
	}
	// "opus" is in both sheets and stored once.
	if strings.Count(sst, ">opus<") != 1 {
		t.Errorf("opus is not shared: %s", sst)
	}
	if !strings.Contains(sst, `count="17" uniqueCount="16"`) {
		t.Errorf("counts: %s", sst[:200])
	}
	book := part(t, data, "xl/workbook.xml")
	if !strings.Contains(book, `<sheet name="Résumé 😀" sheetId="2" r:id="rId2"/>`) {
		t.Errorf("workbook: %s", book)
	}
	// Every part is well-formed XML.
	p, err := opc.Open(data, opc.Limits{})
	must(t, err)
	for _, name := range p.Parts() {
		wellFormed(t, name, part(t, data, name))
	}
}

func wellFormed(t testing.TB, name, s string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("%s is not well-formed: %v", name, err)
		}
	}
}

func TestDeterministic(t *testing.T) {
	a, err := report(t).Write()
	must(t, err)
	b, err := report(t).Write()
	must(t, err)
	if string(a) != string(b) {
		t.Error("the same workbook gave different bytes")
	}
}

func TestNoStringsNoSharedPart(t *testing.T) {
	wb := New()
	sh, err := wb.AddSheet("n")
	must(t, err)
	must(t, sh.AppendRow(Number(1)))
	data, err := wb.Write()
	must(t, err)
	p, err := opc.Open(data, opc.Limits{})
	must(t, err)
	if p.Has("xl/sharedStrings.xml") {
		t.Error("a workbook with no text has a shared strings part")
	}
	if s := part(t, data, "xl/worksheets/sheet1.xml"); !strings.Contains(s, `<dimension ref="A1:A1"/>`) {
		t.Error(s)
	}
	empty := New()
	_, err = empty.AddSheet("e")
	must(t, err)
	data, err = empty.Write()
	must(t, err)
	if s := part(t, data, "xl/worksheets/sheet1.xml"); !strings.Contains(s, `<dimension ref="A1"/>`) {
		t.Error(s)
	}
}

func TestDateSerial(t *testing.T) {
	for _, c := range []struct {
		t    time.Time
		want float64
	}{
		// §18.17.4.1: the base is 1899-12-30, so 1900-03-01 is 61 — the day
		// from which Excel's 1900-is-a-leap-year count agrees.
		{time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC), 61},
		{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 25569},
		{time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), 46321.5},
		// ⛔ Past what a Duration holds: Time.Sub would saturate here.
		{time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), 2958465},
	} {
		got, err := dateSerial(c.t)
		must(t, err)
		if got != c.want {
			t.Errorf("dateSerial(%v) = %v, want %v", c.t, got, c.want)
		}
	}
	for _, bad := range []time.Time{
		time.Date(1900, 2, 28, 23, 0, 0, 0, time.UTC),
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		if _, err := dateSerial(bad); err == nil {
			t.Errorf("dateSerial(%v) was accepted", bad)
		}
	}
}

func TestFormats(t *testing.T) {
	for _, c := range []struct {
		f    Format
		want string
	}{
		{Decimal(0), "#,##0"},
		{Decimal(2), "#,##0.00"},
		{Decimal(-3), "#,##0"},
		{Decimal(99), "#,##0.000000000000000"},
		{Percent(0), "0%"},
		{Percent(2), "0.00%"},
		{General, ""},
	} {
		if c.f.code != c.want {
			t.Errorf("%q, want %q", c.f.code, c.want)
		}
	}
}

func TestSheetNames(t *testing.T) {
	wb := New()
	for _, bad := range []string{
		"", strings.Repeat("x", 32), strings.Repeat("😀", 16), "a:b", `a\b`, "a/b", "a?b", "a*b", "a[b", "a]b",
		"'a", "a'", "history", "a\x01b", "a\x7fb", "\xff", "a￾b",
	} {
		if _, err := wb.AddSheet(bad); err == nil {
			t.Errorf("sheet name %q was accepted", bad)
		}
	}
	for _, good := range []string{strings.Repeat("x", 31), strings.Repeat("😀", 15) + "x", "a'b", "Q3 actuals"} {
		s, err := wb.AddSheet(good)
		if err != nil {
			t.Errorf("sheet name %q was refused: %v", good, err)
			continue
		}
		if s.Name() != good {
			t.Errorf("Name() = %q", s.Name())
		}
	}
	if _, err := wb.AddSheet("q3 ACTUALS"); err == nil {
		t.Error("a name differing only in case was accepted")
	}
}

func TestRefusals(t *testing.T) {
	if _, err := New().Write(); err == nil {
		t.Error("a workbook with no sheet was written")
	}
	wb := New()
	sh, _ := wb.AddSheet("s")
	for _, c := range []Cell{Number(math.NaN()), Number(math.Inf(1)), Number(math.Inf(-1)),
		Date(time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC))} {
		if err := sh.AppendRow(c); err == nil {
			t.Errorf("%v was accepted", c)
		}
	}
	if err := sh.AppendRow(make([]Cell, maxColumns+1)...); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a row past XFD: %v", err)
	}
	for _, c := range []struct {
		col int
		w   float64
	}{{-1, 1}, {maxColumns, 1}, {0, -1}, {0, 256}, {0, math.NaN()}} {
		if err := sh.SetColumnWidth(c.col, c.w); err == nil {
			t.Errorf("SetColumnWidth(%d, %v) was accepted", c.col, c.w)
		}
	}
	must(t, sh.SetColumnWidth(maxColumns-1, 255))
	must(t, sh.AppendRow(Number(1)))
	if err := sh.SetHeader("late"); err == nil {
		t.Error("a header after a row was accepted")
	}
	// A cell is 32 767 characters, as Excel counts them: in UTF-16.
	if err := sh.AppendRow(String(strings.Repeat("x", maxCellChars))); err != nil {
		t.Error(err)
	}
	if err := sh.AppendRow(String(strings.Repeat("😀", maxCellChars/2+1))); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a text past 32 767 UTF-16 units: %v", err)
	}
	if err := New().mustSheet(t).SetHeader(strings.Repeat("x", maxCellChars+1)); err == nil {
		t.Error("an oversized header was accepted")
	}
}

func (w *Workbook) mustSheet(t *testing.T) *Sheet {
	s, err := w.AddSheet("x")
	must(t, err)
	return s
}

// lower sets a limit for one test and puts it back.
func lower(t *testing.T, p *int, v int) {
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func TestLimits(t *testing.T) {
	t.Run("rows", func(t *testing.T) {
		lower(t, &maxRows, 2)
		sh := New().mustSheet(t)
		must(t, sh.AppendRow())
		must(t, sh.AppendRow())
		if err := sh.AppendRow(); !errors.Is(err, ErrTooLarge) {
			t.Error(err)
		}
	})
	t.Run("sheets", func(t *testing.T) {
		lower(t, &maxSheets, 1)
		wb := New()
		wb.mustSheet(t)
		if _, err := wb.AddSheet("y"); !errors.Is(err, ErrTooLarge) {
			t.Error(err)
		}
	})
	t.Run("shared strings", func(t *testing.T) {
		lower(t, &maxSharedStrings, 1)
		wb := New()
		must(t, wb.mustSheet(t).AppendRow(String("a"), String("a"), String("b")))
		if _, err := wb.Write(); !errors.Is(err, ErrTooLarge) {
			t.Error(err)
		}
	})
	styleBytes := len(newStyles().xml())
	t.Run("sheet part, while rows are written", func(t *testing.T) {
		lower(t, &maxPartBytes, 600)
		wb := New()
		sh := wb.mustSheet(t)
		for range 40 {
			must(t, sh.AppendRow(Number(1)))
		}
		if _, err := wb.Write(); !errors.Is(err, ErrTooLarge) {
			t.Error(err)
		}
	})
	t.Run("styles part", func(t *testing.T) {
		lower(t, &maxPartBytes, styleBytes-1)
		wb := New()
		wb.mustSheet(t)
		if _, err := wb.Write(); !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "styles") {
			t.Error(err)
		}
	})
	t.Run("shared strings part", func(t *testing.T) {
		lower(t, &maxPartBytes, styleBytes+10)
		wb := New()
		must(t, wb.mustSheet(t).AppendRow(String(strings.Repeat("x", styleBytes))))
		if _, err := wb.Write(); !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "sharedStrings") {
			t.Error(err)
		}
	})
	t.Run("workbook part", func(t *testing.T) {
		lower(t, &maxPartBytes, styleBytes+10)
		wb := New()
		for i := range 40 {
			_, err := wb.AddSheet(strings.Repeat("s", 28) + columnName(i))
			must(t, err)
		}
		if _, err := wb.Write(); !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "workbook") {
			t.Error(err)
		}
	})
	t.Run("all parts", func(t *testing.T) {
		lower(t, &maxUncompressedBytes, 100) // less than the first sheet
		wb := New()
		wb.mustSheet(t)
		if _, err := wb.Write(); !errors.Is(err, ErrTooLarge) {
			t.Error(err)
		}
	})
	t.Run("package", func(t *testing.T) {
		lower(t, &maxPackageBytes, 100)
		wb := New()
		wb.mustSheet(t)
		if _, err := wb.Write(); !errors.Is(err, ErrTooLarge) {
			t.Error(err)
		}
	})
}

func TestNumber(t *testing.T) {
	for v, want := range map[float64]string{
		0: "0", math.Copysign(0, -1): "0", 1234567: "1234567", -0.5: "-0.5", 1e20: "100000000000000000000",
		1e21: "1E+21", 1e-7: "0.0000001", 1e-9: "1E-09", 0.30000000000000004: "0.30000000000000004",
	} {
		if got := number(v); got != want {
			t.Errorf("number(%v) = %q, want %q", v, got, want)
		}
	}
}

// BenchmarkWrite: 10 000 rows of a report's shape — text, numbers with a
// format, a date — the size of a month of per-request cost lines.
func BenchmarkWrite(b *testing.B) {
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for b.Loop() {
		wb := New()
		sh, _ := wb.AddSheet("Costs")
		_ = sh.SetHeader("Model", "Tokens", "Cost", "Share", "Day")
		for i := range 10000 {
			_ = sh.AppendRow(String([]string{"opus", "sonnet", "haiku"}[i%3]), Number(float64(i*1000)),
				Number(float64(i)*0.0123).Format(Decimal(4)), Number(float64(i%100)/100).Format(Percent(1)), Date(day))
		}
		if _, err := wb.Write(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFormulaLike(t *testing.T) {
	for s, want := range map[string]bool{"": false, "=": true, "+1": true, "-1": true, "@a": true, "a=1": false, " =1": false} {
		if formulaLike(s) != want {
			t.Errorf("formulaLike(%q) != %v", s, want)
		}
	}
}

// FuzzWrite: whatever the cells hold, the result is either a refusal or a
// package that opens, whose every part is well-formed, which our reader
// reads, and whose texts come back as they went in.
func FuzzWrite(f *testing.F) {
	f.Add("name", "text", 1.5, int64(0), true)
	f.Add("=SUM(A1)", "_x0041_\x00\xff", math.NaN(), int64(-1e12), false)
	f.Add("Résumé", "@cmd|' /C calc'!A0", math.Inf(1), int64(253402300799), true)
	f.Add("a]b", "-2+3", -0.0, int64(-2208988800), false)
	f.Fuzz(func(t *testing.T, name, text string, num float64, unix int64, bold bool) {
		wb := New()
		sh, err := wb.AddSheet(name)
		if err != nil {
			sh, _ = wb.AddSheet("fallback")
		}
		hdr := String(text)
		if bold {
			hdr = hdr.Bold()
		}
		_ = sh.SetHeader(text, name)
		rowErr := sh.AppendRow(hdr, Number(num).Format(Decimal(int(unix%20))), Bool(bold),
			Date(time.Unix(unix%(1<<40), 0).UTC()), Cell{}, String(name))
		data, err := wb.Write()
		if err != nil {
			t.Fatalf("Write refused what AddSheet and AppendRow accepted: %v", err)
		}
		p, err := opc.Open(data, opc.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range p.Parts() {
			b, err := p.Part(n)
			if err != nil {
				t.Fatal(err)
			}
			wellFormed(t, n, string(b))
			if strings.Contains(string(b), "<f>") {
				t.Fatalf("%s holds a formula", n)
			}
		}
		doc, err := ooxml.ParseWorkbook(data, opc.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if rowErr != nil {
			return
		}
		tbl := doc.Blocks[len(doc.Blocks)-1].(richdoc.Table)
		last := tbl.Rows[len(tbl.Rows)-1]
		// What goes in as text comes out as text, once invalid UTF-8 has
		// become U+FFFD, the one change made on purpose.
		if got, want := cellText(last[0]), string([]rune(text)); got != want {
			t.Fatalf("text %q came back as %q", want, got)
		}
	})
}
