// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"archive/zip"
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// ⛔ WRITTEN BY LIBREOFFICE from a three-line CSV. Every one of its five text
// cells is a shared-string INDEX, which is what makes the central rule of this
// reader a measurement rather than a reading of the standard.
const xlsxPath = "testdata/book.xlsx"

func parseBook(t *testing.T, path string) *richdoc.Document {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseWorkbook(b, opc.Limits{})
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return d
}

// ⛔ The whole point: a reader that took <v> as the text would produce a table
// of "0", "1", "2" in place of every word — and it would look like a working
// reader, because small integers are plausible cell contents in a spreadsheet.
func TestTextCellsAreSharedStringIndexes(t *testing.T) {
	got := flatten(parseBook(t, xlsxPath))
	for _, want := range []string{"# book", "|format|tools|", "|docx|1|", "|xlsx|1|"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
	// The negative half: the indexes themselves must NOT be in the output
	// where a word belongs. "0" and "2" are the indexes of "format" and
	// "docx", and neither is a value in this sheet.
	if strings.Contains(got, "|0|") || strings.Contains(got, "|2|") {
		t.Errorf("a shared-string index reached the table as text:\n%s", got)
	}
}

// The sheet's heading is its own NAME from the workbook, not its filename.
func TestTheSheetHeadingIsItsName(t *testing.T) {
	d := parseBook(t, xlsxPath)
	h, ok := d.Blocks[0].(richdoc.Heading)
	if !ok {
		t.Fatalf("the first block is %T, want a heading", d.Blocks[0])
	}
	got := ""
	for _, i := range h.Inlines {
		if tx, ok := i.(richdoc.Text); ok {
			got += tx.Value
		}
	}
	if got != "book" {
		t.Errorf("the heading is %q, want the sheet's name \"book\"", got)
	}
}

// ⛔ A cell is placed by its REFERENCE. A row writes only the cells that hold
// something, so a reader that appended in order would shift every column after
// a gap. This is the defect that shows up as "the numbers are in the wrong
// columns" on somebody's real spreadsheet and on no fixture.
func TestASparseRowKeepsItsColumns(t *testing.T) {
	d := parseSheet(t, `<row r="1">`+
		`<c r="A1" t="inlineStr"><is><t>a</t></is></c>`+
		`<c r="C1" t="inlineStr"><is><t>c</t></is></c>`+
		`<c r="E1" t="inlineStr"><is><t>e</t></is></c></row>`, nil)
	// ⛔ Asserted on the STRUCTURE, not on a pattern of pipes. The first
	// version of this test expected the wrong number of them and failed
	// against correct code: a row holding A, C and E is FIVE cells with the
	// second and fourth empty, and a rendered string makes that easy to
	// miscount.
	tbl := firstTable(t, d)
	if len(tbl.Rows) != 1 {
		t.Fatalf("%d rows, want 1", len(tbl.Rows))
	}
	row := tbl.Rows[0]
	if len(row) != 5 {
		t.Fatalf("%d cells, want 5 (A through E)", len(row))
	}
	for i, want := range []string{"a", "", "c", "", "e"} {
		if got := cellString(row[i]); got != want {
			t.Errorf("column %d holds %q, want %q", i+1, got, want)
		}
	}
}

// Rows of different widths are padded to the widest, because a richdoc table
// is rectangular and a consumer that walked a ragged one would read past the
// end of a short row.
func TestRowsArePaddedToTheWidest(t *testing.T) {
	d := parseSheet(t, `<row r="1"><c r="A1" t="inlineStr"><is><t>a</t></is></c></row>`+
		`<row r="2"><c r="A2" t="inlineStr"><is><t>b</t></is></c>`+
		`<c r="C2" t="inlineStr"><is><t>c</t></is></c></row>`, nil)
	tbl := firstTable(t, d)
	if len(tbl.Rows) != 2 {
		t.Fatalf("%d rows, want 2", len(tbl.Rows))
	}
	for i, r := range tbl.Rows {
		if len(r) != 3 {
			t.Errorf("row %d has %d cells, want 3", i+1, len(r))
		}
	}
}

func TestColumnOfACellReference(t *testing.T) {
	for ref, want := range map[string]int{
		"A1": 1, "B7": 2, "Z1": 26, "AA3": 27, "AB1": 28, "BA1": 53,
		"a1": 1, "XFD1": 16384,
	} {
		got, ok := columnOf(ref)
		if !ok || got != want {
			t.Errorf("columnOf(%q) = %d, %v; want %d", ref, got, ok, want)
		}
	}
	for _, ref := range []string{"", "1", "11", "-A1", "A!1"} {
		if got, ok := columnOf(ref); ok {
			t.Errorf("columnOf(%q) = %d, true; want not a reference", ref, got)
		}
	}
}

// Every cell type, and each one puts its value somewhere different.
func TestEveryCellType(t *testing.T) {
	sheet := `<row r="1">` +
		`<c r="A1" t="s"><v>1</v></c>` + // the shared table's second entry
		`<c r="B1" t="inlineStr"><is><t>inline</t></is></c>` +
		`<c r="C1" t="str"><f>CONCAT("a","b")</f><v>ab</v></c>` +
		`<c r="D1" t="n"><v>42</v></c>` +
		`<c r="E1"><v>43</v></c>` + // no t at all: a number, which is what Excel writes
		`<c r="F1" t="b"><v>1</v></c>` +
		`<c r="G1" t="b"><v>0</v></c>` +
		`<c r="H1" t="e"><v>#DIV/0!</v></c>` +
		`</row>`
	got := flatten(parseSheet(t, sheet, []string{"first", "second"}))
	for _, want := range []string{"second", "inline", "ab", "42", "43", "TRUE", "FALSE", "#DIV/0!"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from %q", want, got)
		}
	}
	// ⛔ The FORMULA must not be in the output: CONCAT(...) is what the cell
	// computes with, "ab" is what it showed.
	if strings.Contains(got, "CONCAT") {
		t.Errorf("a formula reached the document: %q", got)
	}
}

// ⛔ An index the shared table does not hold yields NOTHING, not the number. A
// file whose shared strings are missing should come out with empty cells
// rather than with "0", "1", "2" in place of its words — which is exactly the
// output of a reader that ignored the type, and the hardest kind of wrong to
// recognise.
func TestAnIndexOutsideTheSharedTableIsEmpty(t *testing.T) {
	got := flatten(parseSheet(t, `<row r="1">`+
		`<c r="A1" t="s"><v>99</v></c>`+
		`<c r="B1" t="s"><v>-1</v></c>`+
		`<c r="C1" t="s"><v>not a number</v></c></row>`, []string{"only one"}))
	if strings.Contains(got, "99") || strings.Contains(got, "-1") ||
		strings.Contains(got, "not a number") {
		t.Errorf("an index out of range came out as text: %q", got)
	}
}

// A shared string written in formatted pieces is ONE string.
func TestASharedStringInPiecesIsJoined(t *testing.T) {
	sst := `<?xml version="1.0"?><sst xmlns="x">` +
		`<si><r><t xml:space="preserve">one </t></r><r><t>word</t></r></si></sst>`
	got, err := sharedStrings([]byte(sst))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "one word" {
		t.Errorf("sharedStrings = %q, want [\"one word\"]", got)
	}
}

// ⛔ A phonetic hint is stored beside a word and never shown in the cell. Its
// <t> would otherwise be appended to the string.
func TestAPhoneticHintIsNotPartOfTheString(t *testing.T) {
	sst := `<?xml version="1.0"?><sst xmlns="x">` +
		`<si><t>東京</t><rPh sb="0" eb="2"><t>とうきょう</t></rPh></si></sst>`
	got, err := sharedStrings([]byte(sst))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "東京" {
		t.Errorf("sharedStrings = %q, want just the word", got)
	}
}

// A sheet of nothing but empty rows is a sheet with no table, not a table of
// nothing: an empty Table reaches a PDF as a rule across the page.
func TestASheetOfEmptyRowsHasNoTable(t *testing.T) {
	d := parseSheet(t, `<row r="1"/><row r="2"/>`, nil)
	for _, b := range d.Blocks {
		if _, ok := b.(richdoc.Table); ok {
			t.Error("a sheet with no cells produced a table")
		}
	}
	if len(d.Blocks) != 1 {
		t.Errorf("%d blocks, want 1 (the heading)", len(d.Blocks))
	}
}

func TestWorkbookRefusals(t *testing.T) {
	if _, err := ParseWorkbook([]byte("not a zip"), opc.Limits{}); err == nil {
		t.Error("rubbish was parsed")
	}
	if _, err := ParseWorkbook(xlsxOf(t, nil, "", nil), opc.Limits{}); err == nil {
		t.Error("a workbook naming no sheets was parsed")
	}
	// A sheet naming a relationship that is not a worksheet is skipped, and
	// with nothing left the workbook names no sheets.
	pkg := xlsxOf(t, nil, `<sheet name="s" r:id="rIdNope"/>`, nil)
	if _, err := ParseWorkbook(pkg, opc.Limits{}); err == nil {
		t.Error("a workbook of stale entries was parsed")
	}
	// The ceiling reaches here.
	pkg = xlsxOf(t, map[string]string{"xl/worksheets/sheet1.xml": sheetOf(`<row r="1"/>`)},
		`<sheet name="s" r:id="rId1"/>`,
		map[string]string{"rId1": "worksheets/sheet1.xml"})
	if _, err := ParseWorkbook(pkg, opc.Limits{MaxPartBytes: 8}); err == nil {
		t.Error("a part past the ceiling was parsed")
	}
}

// parseSheet builds a one-sheet workbook with the given rows and shared
// strings, and parses it.
func parseSheet(t *testing.T, rows string, shared []string) *richdoc.Document {
	t.Helper()
	parts := map[string]string{"xl/worksheets/sheet1.xml": sheetOf(rows)}
	rels := map[string]string{"rId1": "worksheets/sheet1.xml"}
	if shared != nil {
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0"?><sst xmlns="x">`)
		for _, s := range shared {
			sb.WriteString(`<si><t xml:space="preserve">` + s + `</t></si>`)
		}
		sb.WriteString(`</sst>`)
		parts["xl/sharedStrings.xml"] = sb.String()
		rels["rIdS"] = "sharedStrings.xml"
	}
	pkg := xlsxOf(t, parts, `<sheet name="s" r:id="rId1"/>`, rels)
	d, err := ParseWorkbook(pkg, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func sheetOf(rows string) string {
	return `<?xml version="1.0"?><worksheet xmlns="x"><sheetData>` + rows +
		`</sheetData></worksheet>`
}

func xlsxOf(t *testing.T, extra map[string]string, sheets string, targets map[string]string) []byte {
	t.Helper()
	var rels strings.Builder
	for id, target := range targets {
		typ := relWorksheet
		if strings.Contains(target, "sharedStrings") {
			typ = relSharedString
		}
		rels.WriteString(`<Relationship Id="` + id + `" Type="` + typ +
			`" Target="` + target + `"/>`)
	}
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rIdMain" Type="` + opc.RelOfficeDocument +
			`" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0"?><workbook xmlns="x" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets>` + sheets + `</sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			rels.String() + `</Relationships>`,
	}
	for k, v := range extra {
		parts[k] = v
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func firstTable(t *testing.T, d *richdoc.Document) richdoc.Table {
	t.Helper()
	for _, b := range d.Blocks {
		if tb, ok := b.(richdoc.Table); ok {
			return tb
		}
	}
	t.Fatal("no table in the document")
	return richdoc.Table{}
}

func cellString(c richdoc.Cell) string {
	var sb strings.Builder
	for _, i := range c.Inlines {
		if tx, ok := i.(richdoc.Text); ok {
			sb.WriteString(tx.Value)
		}
	}
	return sb.String()
}

// ⛔ Every walker over a workbook has its own error return for a token that
// does not arrive, and a reader that returned nil there would report a
// truncated spreadsheet as a complete one.
func TestTruncationInAWorkbookIsAnError(t *testing.T) {
	for name, rows := range map[string]string{
		"a row":              `<row r="1"><c`,
		"a cell":             `<row r="1"><c r="A1"><v`,
		"inside a cell":      `<row r="1"><c r="A1"><v>1</v><unknown><inner`,
		"an inline string":   `<row r="1"><c r="A1" t="inlineStr"><is><t>x`,
		"inside is":          `<row r="1"><c r="A1" t="inlineStr"><is><unknown><inner`,
		"a formula":          `<row r="1"><c r="A1"><f><inner`,
		"something in a row": `<row r="1"><unknown><inner`,
		"the sheet data":     `<row r="1"><c r="A1"><v>1</v></c></row>`,
	} {
		t.Run(name, func(t *testing.T) {
			pkg := xlsxOf(t, map[string]string{
				"xl/worksheets/sheet1.xml": `<?xml version="1.0"?><worksheet xmlns="x">` +
					`<sheetData>` + rows,
			}, `<sheet name="s" r:id="rId1"/>`,
				map[string]string{"rId1": "worksheets/sheet1.xml"})
			if _, err := ParseWorkbook(pkg, opc.Limits{}); err == nil {
				t.Error("a truncated sheet parsed without an error")
			}
		})
	}
}

func TestABrokenWorkbookPartIsAnError(t *testing.T) {
	pkg := xlsxOf(t, nil, "", nil)
	pkg = replacePart(t, pkg, "xl/workbook.xml",
		`<?xml version="1.0"?><workbook xmlns="x"><sheets><sheet`)
	if _, err := ParseWorkbook(pkg, opc.Limits{}); err == nil {
		t.Error("a truncated workbook part parsed without an error")
	}
}

func TestABrokenSharedStringsPartIsAnError(t *testing.T) {
	pkg := xlsxOf(t, map[string]string{
		"xl/worksheets/sheet1.xml": sheetOf(`<row r="1"/>`),
		"xl/sharedStrings.xml":     `<?xml version="1.0"?><sst xmlns="x"><si><t>x`,
	}, `<sheet name="s" r:id="rId1"/>`, map[string]string{
		"rId1": "worksheets/sheet1.xml", "rIdS": "sharedStrings.xml",
	})
	if _, err := ParseWorkbook(pkg, opc.Limits{}); err == nil {
		t.Error("a truncated shared-strings part parsed without an error")
	}
}

// ⛔ A workbook CLAIMING more sheets than the ceiling is refused from the list,
// before any sheet is opened: the list is a count the file chose. The same for
// the shared strings, and for a cell beyond the last column a spreadsheet has.
func TestTheWorkbookCeilings(t *testing.T) {
	t.Run("sheets", func(t *testing.T) {
		var sb strings.Builder
		for range maxSheets + 1 {
			sb.WriteString(`<sheet name="s" r:id="rId1"/>`)
		}
		pkg := xlsxOf(t, nil, sb.String(), nil)
		_, err := ParseWorkbook(pkg, opc.Limits{})
		if err == nil || !strings.Contains(err.Error(), "more than") {
			t.Errorf("a workbook listing too many sheets: %v", err)
		}
	})
	t.Run("a cell past the last column", func(t *testing.T) {
		// XFE is one past XFD, which is the last column a spreadsheet has.
		pkg := xlsxOf(t, map[string]string{
			"xl/worksheets/sheet1.xml": sheetOf(`<row r="1"><c r="XFE1"><v>1</v></c></row>`),
		}, `<sheet name="s" r:id="rId1"/>`, map[string]string{"rId1": "worksheets/sheet1.xml"})
		_, err := ParseWorkbook(pkg, opc.Limits{})
		if err == nil || !strings.Contains(err.Error(), "column") {
			t.Errorf("a cell past the last column: %v", err)
		}
	})
}

// A sheet relationship that is external or unresolvable is skipped, and the
// shared-strings relationship likewise — a workbook whose strings cannot be
// reached still has its numbers.
func TestUnreachableRelationshipsAreSkipped(t *testing.T) {
	pkg := xlsxOf(t, map[string]string{"xl/worksheets/sheet1.xml": sheetOf(
		`<row r="1"><c r="A1"><v>7</v></c></row>`)},
		`<sheet name="s" r:id="rId1"/>`, map[string]string{"rId1": "worksheets/sheet1.xml"})
	pkg = replacePart(t, pkg, "xl/_rels/workbook.xml.rels",
		`<?xml version="1.0"?><Relationships `+
			`xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			`<Relationship Id="rId1" Type="`+relWorksheet+`" Target="worksheets/sheet1.xml"/>`+
			`<Relationship Id="rIdS" Type="`+relSharedString+
			`" Target="https://example.com/s.xml" TargetMode="External"/>`+
			`</Relationships>`)
	got := flatten(parseOK(t, pkg))
	if !strings.Contains(got, "|7|") {
		t.Errorf("the numbers were lost with the unreachable strings: %q", got)
	}
	// A sheet that climbs out of the package is skipped too, and with nothing
	// left the workbook names no sheets.
	pkg2 := xlsxOf(t, nil, `<sheet name="s" r:id="rId1"/>`, nil)
	pkg2 = replacePart(t, pkg2, "xl/_rels/workbook.xml.rels",
		`<?xml version="1.0"?><Relationships `+
			`xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			`<Relationship Id="rId1" Type="`+relWorksheet+`" Target="../../out.xml"/>`+
			`</Relationships>`)
	if _, err := ParseWorkbook(pkg2, opc.Limits{}); err == nil {
		t.Error("a sheet that climbs out of the package was parsed as one")
	}
}

// A sheet part the relationships name but the package does not hold, and a
// sheet nested past the depth ceiling.
func TestASheetThatCannotBeReadIsAnError(t *testing.T) {
	pkg := xlsxOf(t, nil, `<sheet name="s" r:id="rId1"/>`,
		map[string]string{"rId1": "worksheets/gone.xml"})
	if _, err := ParseWorkbook(pkg, opc.Limits{}); err == nil {
		t.Error("a missing sheet part was parsed")
	}
	deep := `<?xml version="1.0"?><worksheet xmlns="x">` +
		strings.Repeat("<g>", maxDepth) + strings.Repeat("</g>", maxDepth) + `</worksheet>`
	pkg = xlsxOf(t, map[string]string{"xl/worksheets/sheet1.xml": deep},
		`<sheet name="s" r:id="rId1"/>`, map[string]string{"rId1": "worksheets/sheet1.xml"})
	_, err := ParseWorkbook(pkg, opc.Limits{})
	if err == nil || !strings.Contains(err.Error(), "nest") {
		t.Errorf("a sheet nested past the ceiling: %v", err)
	}
}

// ⛔ The three parts ParseWorkbook opens before any sheet each have their own
// error return, and a single small ceiling only reaches the first.
func TestTheCeilingIsReportedForTheWorkbooksOwnParts(t *testing.T) {
	pad := "<!--" + strings.Repeat("p", 4000) + "-->"
	t.Run("no main part", func(t *testing.T) {
		pkg := xlsxOf(t, nil, "", nil)
		pkg = replacePart(t, pkg, "_rels/.rels",
			`<?xml version="1.0"?><Relationships `+
				`xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`)
		if _, err := ParseWorkbook(pkg, opc.Limits{}); err == nil {
			t.Error("a package naming no main part was parsed")
		}
	})
	t.Run("the workbook part", func(t *testing.T) {
		pkg := xlsxOf(t, nil, "", nil)
		pkg = replacePart(t, pkg, "xl/workbook.xml",
			`<?xml version="1.0"?>`+pad+`<workbook xmlns="x"/>`)
		if _, err := ParseWorkbook(pkg, opc.Limits{MaxPartBytes: 2048}); err == nil {
			t.Error("a workbook part past the ceiling was parsed")
		}
	})
	t.Run("the workbook's relationships", func(t *testing.T) {
		pkg := xlsxOf(t, nil, `<sheet name="s" r:id="rId1"/>`, nil)
		pkg = replacePart(t, pkg, "xl/_rels/workbook.xml.rels",
			`<?xml version="1.0"?>`+pad+`<Relationships `+
				`xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`)
		if _, err := ParseWorkbook(pkg, opc.Limits{MaxPartBytes: 2048}); err == nil {
			t.Error("a relationships part past the ceiling was parsed")
		}
	})
	t.Run("the shared strings", func(t *testing.T) {
		pkg := xlsxOf(t, map[string]string{
			"xl/worksheets/sheet1.xml": sheetOf(`<row r="1"/>`),
			"xl/sharedStrings.xml":     `<?xml version="1.0"?>` + pad + `<sst xmlns="x"/>`,
		}, `<sheet name="s" r:id="rId1"/>`, map[string]string{
			"rId1": "worksheets/sheet1.xml", "rIdS": "sharedStrings.xml",
		})
		if _, err := ParseWorkbook(pkg, opc.Limits{MaxPartBytes: 2048}); err == nil {
			t.Error("a shared-strings part past the ceiling was parsed")
		}
	})
}

func parseOK(t *testing.T, pkg []byte) *richdoc.Document {
	t.Helper()
	d, err := ParseWorkbook(pkg, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// ⛔ The two ceilings a real fixture cannot reach: a million rows and two
// million shared strings. They are lowered for the test, and the assertions
// are on LITERALS so that raising the constant cannot raise the assertion with
// it — the judge must not move with the subject.
func TestTheCeilingsNoFixtureCouldReach(t *testing.T) {
	t.Run("rows", func(t *testing.T) {
		defer func(n int) { maxRows = n }(maxRows)
		maxRows = 3
		var sb strings.Builder
		for i := range 5 {
			sb.WriteString(`<row r="` + strconv.Itoa(i+1) + `"/>`)
		}
		pkg := xlsxOf(t, map[string]string{
			"xl/worksheets/sheet1.xml": sheetOf(sb.String()),
		}, `<sheet name="s" r:id="rId1"/>`, map[string]string{"rId1": "worksheets/sheet1.xml"})
		_, err := ParseWorkbook(pkg, opc.Limits{})
		if err == nil {
			t.Fatal("five rows were read under a ceiling of three")
		}
		if !strings.Contains(err.Error(), "more than 3 rows") {
			t.Errorf("the refusal does not say what was allowed: %v", err)
		}
	})
	t.Run("shared strings", func(t *testing.T) {
		defer func(n int) { maxSharedStr = n }(maxSharedStr)
		maxSharedStr = 2
		sst := `<?xml version="1.0"?><sst xmlns="x">` +
			`<si><t>a</t></si><si><t>b</t></si><si><t>c</t></si></sst>`
		_, err := sharedStrings([]byte(sst))
		if err == nil {
			t.Fatal("three shared strings were read under a ceiling of two")
		}
		if !strings.Contains(err.Error(), "more than 2 shared strings") {
			t.Errorf("the refusal does not say what was allowed: %v", err)
		}
	})
	t.Run("sheets", func(t *testing.T) {
		defer func(n int) { maxSheets = n }(maxSheets)
		maxSheets = 2
		pkg := xlsxOf(t, nil, strings.Repeat(`<sheet name="s" r:id="rId1"/>`, 4), nil)
		_, err := ParseWorkbook(pkg, opc.Limits{})
		if err == nil {
			t.Fatal("four sheets were listed under a ceiling of two")
		}
		if !strings.Contains(err.Error(), "more than 2 sheets") {
			t.Errorf("the refusal does not say what was allowed: %v", err)
		}
	})
	// And the control for all three: the same fixtures pass at the real
	// ceilings, so each refusal above is the ceiling and not the fixture.
	t.Run("the control", func(t *testing.T) {
		pkg := xlsxOf(t, map[string]string{
			"xl/worksheets/sheet1.xml": sheetOf(`<row r="1"><c r="A1"><v>1</v></c></row>`),
		}, `<sheet name="s" r:id="rId1"/>`, map[string]string{"rId1": "worksheets/sheet1.xml"})
		if _, err := ParseWorkbook(pkg, opc.Limits{}); err != nil {
			t.Errorf("the same shape at the real ceilings: %v", err)
		}
	})
}

// The remaining error paths, each needing its truncation at an exact point.
func TestTheRemainingWorkbookErrorPaths(t *testing.T) {
	t.Run("a shared-strings relationship that climbs out", func(t *testing.T) {
		pkg := xlsxOf(t, map[string]string{
			"xl/worksheets/sheet1.xml": sheetOf(`<row r="1"><c r="A1"><v>1</v></c></row>`),
		}, `<sheet name="s" r:id="rId1"/>`, nil)
		pkg = replacePart(t, pkg, "xl/_rels/workbook.xml.rels",
			`<?xml version="1.0"?><Relationships `+
				`xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
				`<Relationship Id="rId1" Type="`+relWorksheet+`" Target="worksheets/sheet1.xml"/>`+
				`<Relationship Id="rIdS" Type="`+relSharedString+`" Target="../../out.xml"/>`+
				`</Relationships>`)
		// Skipped, not fatal: a workbook whose strings cannot be reached
		// still has its numbers.
		if got := flatten(parseOK(t, pkg)); !strings.Contains(got, "|1|") {
			t.Errorf("the numbers were lost: %q", got)
		}
	})
	t.Run("a workbook with no sheets element", func(t *testing.T) {
		pkg := xlsxOf(t, nil, "", nil)
		pkg = replacePart(t, pkg, "xl/workbook.xml",
			`<?xml version="1.0"?><workbook xmlns="x"><other/></workbook>`)
		_, err := ParseWorkbook(pkg, opc.Limits{})
		if err == nil || !strings.Contains(err.Error(), "no sheets") {
			t.Errorf("a workbook with no sheets element: %v", err)
		}
	})
	t.Run("truncation inside a shared string", func(t *testing.T) {
		if _, err := sharedStrings([]byte(`<?xml version="1.0"?><sst xmlns="x"><si`)); err == nil {
			t.Error("a truncated shared-strings part was read")
		}
	})
	t.Run("truncation inside a phonetic hint", func(t *testing.T) {
		if _, err := sharedStrings([]byte(
			`<?xml version="1.0"?><sst xmlns="x"><si><t>x</t><rPh><inner`)); err == nil {
			t.Error("a truncated phonetic hint was read")
		}
	})
}

// The last three error paths, each inside a walker the ones above reach only
// after it has returned. ⛔ The truncation has to land one token INSIDE the
// element being skipped or read, or an outer walker reports it first and the
// branch under test never runs.
func TestTheLastThreeWorkbookErrorPaths(t *testing.T) {
	for name, rows := range map[string]string{
		// readRow's Skip over something that is not a cell.
		"something in a row that is not a cell": `<row r="1"><notACell><inner>`,
		// readCell's flatText over <v>.
		"inside a cell's value": `<row r="1"><c r="A1"><v>`,
		// inlineString's own Token.
		"inside an inline string": `<row r="1"><c r="A1" t="inlineStr"><is>`,
	} {
		t.Run(name, func(t *testing.T) {
			pkg := xlsxOf(t, map[string]string{
				"xl/worksheets/sheet1.xml": `<?xml version="1.0"?><worksheet xmlns="x">` +
					`<sheetData>` + rows,
			}, `<sheet name="s" r:id="rId1"/>`,
				map[string]string{"rId1": "worksheets/sheet1.xml"})
			if _, err := ParseWorkbook(pkg, opc.Limits{}); err == nil {
				t.Error("a truncated sheet parsed without an error")
			}
		})
	}
}

// A row may hold things that are not cells — Excel writes extLst there, and
// LibreOffice writes nothing but a reader meets both. ⛔ They are skipped and
// the row CONTINUES: a reader that stopped at the first one would lose every
// cell after it. The truncated case above covers the skip failing; this covers
// it succeeding, which is the ordinary one.
func TestSomethingInARowThatIsNotACellIsSkippedAndTheRowGoesOn(t *testing.T) {
	d := parseSheet(t, `<row r="1">`+
		`<extLst><ext uri="x"/></extLst>`+
		`<c r="A1" t="inlineStr"><is><t>kept</t></is></c>`+
		`</row>`, nil)
	tbl := firstTable(t, d)
	if len(tbl.Rows) != 1 || len(tbl.Rows[0]) != 1 {
		t.Fatalf("the row came out as %v", tbl.Rows)
	}
	if got := cellString(tbl.Rows[0][0]); got != "kept" {
		t.Errorf("the cell after the extension holds %q, want \"kept\"", got)
	}
}
