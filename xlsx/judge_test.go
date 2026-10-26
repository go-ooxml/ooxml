// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package xlsx

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ⛔ THE JUDGES THAT ARE NOT OUR OWN CODE.
//
// The round trip proves that this writer and the parent package's reader
// agree, and two halves of one repository agreeing is the weakest evidence
// there is. So two readers other people wrote from the same standard are
// asked, where they are installed: LibreOffice, which renders the cells as a
// person sees them (formats applied), and openpyxl, the reader most Python
// programs use, which reports types, formats, fonts and the frozen pane.
//
// Both SKIP where their program is absent, which includes CI. A skipped test
// is not a gate, so the README records what they said and when.

func writeReport(t *testing.T) string {
	t.Helper()
	data, err := report(t).Write()
	must(t, err)
	path := filepath.Join(t.TempDir(), "report.xlsx")
	must(t, os.WriteFile(path, data, 0o644))
	return path
}

func TestLibreOfficeReadsWhatWeWrote(t *testing.T) {
	soffice := findSoffice()
	if soffice == "" {
		t.Skip("no LibreOffice here")
	}
	path := writeReport(t)
	dir := filepath.Dir(path)
	// ⛔ The ninth filter token, "save cell contents as shown", is what makes
	// the CSV show the FORMATS. Without it LibreOffice writes the raw values
	// in a style of its own choosing, and a format this writer got wrong would
	// be invisible.
	const asShown = `csv:Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,true`
	cmd := exec.Command(soffice, "--headless", "--convert-to", asShown, "--outdir", dir, path)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("LibreOffice refused the workbook: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report.csv"))
	must(t, err)
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	t.Logf("LibreOffice:\n%s", raw)
	want := [][]string{
		{"Model", "Tokens", "Cost", "Share", "Day", "Billed", "Note"},
		// The formats applied: thousands and two places, a percentage with
		// one, the date on its own wall clock — and "=1+1" still text.
		{"opus", "1234567", "1,234.50", "42.5%", "2026-10-26", "TRUE", "=1+1"},
		// 1E+021 is LibreOffice's General for 1e21.
		{"sonnet", "0", "1E+021", "0%", "2026-10-26 23:30:00", "FALSE", "_x0041_ and \x01"},
		{"", "", "", "", "", "", ""},
		{"Total", "", "1,234.50", "", "", "", "  spaced  "},
	}
	if len(rows) != len(want) {
		t.Fatalf("%d rows, want %d", len(rows), len(want))
	}
	for r := range want {
		for c := range want[r] {
			if c >= len(rows[r]) || rows[r][c] != want[r][c] {
				t.Errorf("row %d col %d: LibreOffice shows %q, want %q", r+1, c+1, at(rows[r], c), want[r][c])
			}
		}
	}
}

func at(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return "<missing>"
}

// openpyxlScript is the program under test's driver: it only reads the file
// and prints what openpyxl says about every cell.
const openpyxlScript = `
import json, sys, openpyxl
wb = openpyxl.load_workbook(sys.argv[1])
out = []
for ws in wb.worksheets:
    cells = []
    for row in ws.iter_rows():
        for c in row:
            if c.value is None:
                continue
            cells.append({"ref": c.coordinate, "value": str(c.value), "type": c.data_type,
                          "format": c.number_format, "bold": bool(c.font.b),
                          "quote": bool(c.quotePrefix)})
    widths = {k: v.width for k, v in ws.column_dimensions.items() if v.customWidth}
    out.append({"name": ws.title, "freeze": ws.freeze_panes, "widths": widths, "cells": cells})
print(json.dumps(out))
`

type opxCell struct {
	Ref, Value, Type, Format string
	Bold, Quote              bool
}

type opxSheet struct {
	Name   string
	Freeze *string
	Widths map[string]float64
	Cells  []opxCell
}

func TestOpenpyxlReadsWhatWeWrote(t *testing.T) {
	uvx, err := exec.LookPath("uvx")
	if err != nil || os.Getenv("XLSX_JUDGE_OPENPYXL") == "" {
		t.Skip("set XLSX_JUDGE_OPENPYXL=1 where uvx can fetch openpyxl")
	}
	path := writeReport(t)
	cmd := exec.Command(uvx, "--quiet", "--with", "openpyxl", "python", "-I", "-c", openpyxlScript, path)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("openpyxl refused the workbook: %v\n%s", err, out)
	}
	var sheets []opxSheet
	if err := json.Unmarshal(out, &sheets); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(sheets) != 2 || sheets[0].Name != "Costs" || sheets[1].Name != "Résumé 😀" {
		t.Fatalf("sheets: %+v", sheets)
	}
	if f := sheets[0].Freeze; f == nil || *f != "A2" {
		t.Errorf("the header is not frozen: %v", f)
	}
	if sheets[1].Freeze != nil {
		t.Errorf("the second sheet is frozen at %v", *sheets[1].Freeze)
	}
	if w := sheets[0].Widths; w["A"] != 28.5 || w["G"] != 40 {
		t.Errorf("widths: %v", w)
	}
	byRef := map[string]opxCell{}
	for _, c := range sheets[0].Cells {
		byRef[c.Ref] = c
	}
	for _, w := range []opxCell{
		{Ref: "A1", Value: "Model", Type: "s", Format: "General", Bold: true},
		{Ref: "B2", Value: "1234567", Type: "n", Format: "General"},
		{Ref: "C2", Value: "1234.5", Type: "n", Format: "#,##0.00"},
		{Ref: "D2", Value: "0.425", Type: "n", Format: "0.0%"},
		{Ref: "E2", Value: "2026-10-26 23:30:00", Type: "d", Format: "yyyy-mm-dd"},
		{Ref: "F2", Value: "True", Type: "b", Format: "General"},
		// ⛔ A string, not a formula ("f"), and marked so an edit keeps it one.
		{Ref: "G2", Value: "=1+1", Type: "s", Format: "General", Quote: true},
		// ⛔ openpyxl 3.1.5 does NOT decode _xHHHH_ in shared strings:
		// reader/strings.py only deletes "x005F_". So the text that merely
		// looked like an escape comes back right, and the control character
		// comes back spelled _x0001_ — openpyxl's gap, which LibreOffice (above)
		// and §22.9.2.19 do not share. Recorded here so a change in either is
		// seen.
		{Ref: "G3", Value: "_x0041_ and _x0001_", Type: "s", Format: "General"},
		{Ref: "E3", Value: "2026-10-26 23:30:00", Type: "d", Format: "yyyy-mm-dd hh:mm:ss"},
		{Ref: "A5", Value: "Total", Type: "s", Format: "General", Bold: true},
		{Ref: "C5", Value: "1234.5", Type: "n", Format: "#,##0.00", Bold: true},
		{Ref: "G5", Value: "  spaced  ", Type: "s", Format: "General"},
	} {
		if got := byRef[w.Ref]; got != w {
			t.Errorf("%s: openpyxl says %+v, want %+v", w.Ref, got, w)
		}
	}
	for _, c := range sheets[1].Cells {
		if c.Type != "s" {
			t.Errorf("%s %q is %q, not a string", c.Ref, c.Value, c.Type)
		}
	}
}

// findSoffice is where LibreOffice is, or "" if it is not here. The macOS
// bundle is not on PATH, which is why the name alone is not enough.
func findSoffice() string {
	if p, err := exec.LookPath("soffice"); err == nil {
		return p
	}
	if runtime.GOOS == "darwin" {
		const bundled = "/Applications/LibreOffice.app/Contents/MacOS/soffice"
		if _, err := os.Stat(bundled); err == nil {
			return bundled
		}
	}
	return ""
}
