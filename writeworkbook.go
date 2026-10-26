// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-ooxml/ooxml/internal/xstring"
	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// The content types and relationship types a .xlsx is made of.
const (
	ctWorkbook = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
	ctSheet    = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"

	// nsS is SpreadsheetML's namespace, and nsR the relationship namespace a
	// workbook needs in order to point at its sheets.
	nsS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	nsR = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

	// sheetNameLimit is how long a sheet name may be. Excel refuses a longer
	// one and refuses the whole file with it.
	sheetNameLimit = 31
)

// WriteWorkbook writes a document's tables as a SpreadsheetML package
// (.xlsx), one sheet per table.
//
// ⛔ Only the tables. A spreadsheet is a grid of cells and a document is not,
// so the headings and paragraphs around a table have nowhere to go — and
// putting them in column A of a sheet of their own would make a spreadsheet
// that looks like a document and works as neither. A document with no tables
// in it gives a workbook with one empty sheet rather than an error, because
// "there was nothing to put in a grid" is an answer.
func WriteWorkbook(d *richdoc.Document) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("ooxml: no document to write")
	}
	tables := tablesIn(d.Blocks)
	parts := []opc.NewPart{}
	rels := map[string][]opc.Rel{
		"": {{ID: "rId1", Type: opc.RelOfficeDocument, Target: "xl/workbook.xml"}},
	}
	var sheetRels []opc.Rel
	names := make([]string, 0, len(tables))
	taken := map[string]bool{}
	for i, t := range tables {
		name := sheetName(i, taken)
		names = append(names, name)
		part := fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		parts = append(parts, opc.NewPart{Name: part, ContentType: ctSheet, Data: sheetXML(t)})
		sheetRels = append(sheetRels, opc.Rel{
			ID:     "rId" + strconv.Itoa(i+1),
			Type:   relWorksheet,
			Target: fmt.Sprintf("worksheets/sheet%d.xml", i+1),
		})
	}
	parts = append(parts, opc.NewPart{
		Name: "xl/workbook.xml", ContentType: ctWorkbook, Data: workbookXML(names),
	})
	rels["xl/workbook.xml"] = sheetRels
	return opc.Build(parts, rels)
}

// tablesIn is every table in a document, descending into the blocks that hold
// blocks — a table inside a list item or a quotation is still a table, and a
// walk that only looked at the top level would miss it.
//
// A document with none gives one empty table, so that the workbook has the
// sheet every workbook needs.
func tablesIn(blocks []richdoc.Block) []richdoc.Table {
	var out []richdoc.Table
	var walk func([]richdoc.Block)
	walk = func(blocks []richdoc.Block) {
		for _, blk := range blocks {
			switch n := blk.(type) {
			case richdoc.Table:
				out = append(out, n)
				// ⛔ And into its cells: a table inside a cell of another
				// table is rare and real, and dropping it loses data rather
				// than formatting.
				for _, row := range append([][]richdoc.Cell{n.Header}, n.Rows...) {
					for _, c := range row {
						walk(c.Content())
					}
				}
			case richdoc.BlockQuote:
				walk(n.Blocks)
			case richdoc.List:
				for _, item := range n.Items {
					walk(item.Blocks)
				}
			}
		}
	}
	walk(blocks)
	if len(out) == 0 {
		return []richdoc.Table{{}}
	}
	return out
}

// sheetName is what to call the i'th sheet.
//
// ⛔ Excel refuses a name longer than 31 characters, one containing any of
// : \ / ? * [ ], and two sheets with the same name — and it refuses the whole
// FILE, not the name. So the name is built rather than taken: "Table 1",
// "Table 2", which is short, legal and unique by construction. taken is here
// so that this stays true if the names ever come from the document.
func sheetName(i int, taken map[string]bool) string {
	// "Table " and a decimal integer is at most 25 characters even for the
	// largest int there is, so the base can never be over the limit and
	// nothing truncates it. The limit still bounds the suffix below, where a
	// collision CAN make a name grow.
	base := "Table " + strconv.Itoa(i+1)
	name := base
	for n := 2; taken[strings.ToLower(name)]; n++ {
		// Excel compares sheet names without case, so "table 1" and "Table 1"
		// are the same name to it.
		suffix := " (" + strconv.Itoa(n) + ")"
		cut := sheetNameLimit - len(suffix)
		if cut > len(base) {
			cut = len(base)
		}
		name = base[:cut] + suffix
	}
	taken[strings.ToLower(name)] = true
	return name
}

// workbookXML writes xl/workbook.xml: the list of sheets, each pointing at
// its own part through a relationship.
func workbookXML(names []string) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<workbook xmlns=%q xmlns:r=%q><sheets>`, nsS, nsR)
	for i, name := range names {
		b.WriteString(`<sheet name="`)
		esc(&b, name)
		fmt.Fprintf(&b, `" sheetId="%d" r:id="rId%d"/>`, i+1, i+1)
	}
	b.WriteString(`</sheets></workbook>`)
	return b.Bytes()
}

// sheetXML writes one worksheet.
func sheetXML(t richdoc.Table) []byte {
	rows := append([][]richdoc.Cell{}, t.Rows...)
	if len(t.Header) > 0 {
		rows = append([][]richdoc.Cell{t.Header}, rows...)
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<worksheet xmlns=%q><sheetData>`, nsS)
	for r, row := range rows {
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		for c, cell := range row {
			text := sheetCellText(cell)
			if text == "" {
				// A cell with nothing in it is left out. A sheet says where
				// each cell is, so an empty one carries no information and
				// Excel writes none either.
				continue
			}
			// ⛔ t="inlineStr" with the text in the cell, rather than a
			// number into a shared strings part. A shared strings table is
			// how Excel saves space on a sheet that repeats itself, and it is
			// a second part that has to agree with this one about every index.
			// Inline strings cost bytes and cannot disagree with anything.
			fmt.Fprintf(&b, `<c r="%s%d" t="inlineStr"><is><t xml:space="preserve">`,
				columnName(c), r+1)
			// ⛔ An ST_Xstring (§22.9.2.19): a control character is spelled
			// _xHHHH_, and text that only looks like such a spelling has its
			// underscore escaped, or a reader would decode it.
			esc(&b, xstring.Encode(text))
			b.WriteString(`</t></is></c>`)
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.Bytes()
}

// sheetCellText is a cell's words, through richdoc's own rule.
func sheetCellText(c richdoc.Cell) string {
	blocks := c.Content()
	if len(blocks) == 0 {
		return ""
	}
	// A cell holding several blocks becomes one cell: a spreadsheet cell has
	// no paragraphs, and the blocks' text joined by a space is what a reader
	// of the sheet can see.
	parts := make([]string, 0, len(blocks))
	for _, blk := range blocks {
		if s := textOf(blk); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// columnName is a spreadsheet column's letters: A to Z, then AA to AZ, BA and
// so on.
//
// ⛔ This IS the spreadsheet column spelling, and it is not the one PDF page
// labels use — there the 27th is "AA" because the letter repeats, here it is
// "AA" because the number carries. The two look alike at 27 and part company
// at 28: BB there, AB here. [go-pdfkit/ops] has the other one, and a comment
// on each says so, because somebody will one day move code between them.
//
// [go-pdfkit/ops]: https://pkg.go.dev/github.com/go-pdfkit/ops
func columnName(n int) string {
	var out []byte
	for {
		out = append([]byte{byte('A' + n%26)}, out...)
		n = n/26 - 1
		if n < 0 {
			return string(out)
		}
	}
}
