// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// The relationship types a workbook points through.
const (
	relWorksheet    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"
	relSharedString = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings"
)

// The ceilings. Each bounds an allocation made from a number the file chose.
//
// ⛔ They are var rather than const so that a test can LOWER them. A ceiling
// of a million rows is a guard no test can reach — a fixture of a million rows
// is minutes of CPU and a hundred megabytes of XML — so it would ship
// unexercised, which is the same as shipping without it. go-odf/odf arrived at
// the same shape for the same reason, and the tests there assert against
// LITERALS so that raising a constant cannot raise the assertion with it.
var (
	maxSheets    = 4096
	maxRows      = 1 << 20 // a sheet's own limit in Excel is 1 048 576
	maxColumns   = 16384   // and XFD is its last column
	maxSharedStr = 1 << 21
)

// ParseWorkbook reads a SpreadsheetML package (.xlsx) into a document: each
// sheet as a heading of its own name, followed by its cells as a table.
//
// ⛔ A cell's value is OFTEN NOT IN THE CELL. With t="s" — which is what every
// piece of text in a real workbook uses — the <v> holds an INDEX into
// xl/sharedStrings.xml, so a reader that took <v> as the text produces a table
// of "0", "1", "2" … in place of every word. Measured on a file LibreOffice
// wrote from a three-line CSV: every one of its five text cells was an index.
func ParseWorkbook(src []byte, lim opc.Limits) (*richdoc.Document, error) {
	p, err := opc.Open(src, lim)
	if err != nil {
		return nil, err
	}
	main, err := p.Main()
	if err != nil {
		return nil, err
	}
	book, err := p.Part(main)
	if err != nil {
		return nil, err
	}
	sheets, err := sheetList(book)
	if err != nil {
		return nil, err
	}
	rels, err := p.Rels(main)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]opc.Rel, len(rels))
	shared := []string(nil)
	for _, r := range rels {
		byID[r.ID] = r
		if opc.SameRelType(r.Type, relSharedString) && !r.External {
			name, err := p.Resolve(main, r)
			if err != nil {
				continue
			}
			data, err := p.Part(name)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			shared, err = sharedStrings(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	var blocks []richdoc.Block
	read := 0
	for _, s := range sheets {
		r, ok := byID[s.relID]
		if !ok || !opc.SameRelType(r.Type, relWorksheet) || r.External {
			continue
		}
		name, err := p.Resolve(main, r)
		if err != nil {
			continue
		}
		data, err := p.Part(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := checkDepth(data); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		rows, err := sheetRows(data, shared)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		read++
		// ⛔ The heading is the sheet's own NAME from the workbook, not its
		// part's filename: "Q3 actuals" is in xl/workbook.xml and
		// "sheet2.xml" is on disk, and only one of them is what the person
		// who made the file called it.
		blocks = append(blocks, richdoc.Heading{
			Level:   1,
			Inlines: []richdoc.Inline{richdoc.Text{Value: s.name}},
		})
		if len(rows) > 0 {
			blocks = append(blocks, richdoc.Table{Rows: rows})
		}
	}
	if read == 0 {
		return nil, fmt.Errorf("ooxml: the workbook names no sheets")
	}
	return &richdoc.Document{Blocks: blocks}, nil
}

type sheetRef struct {
	name  string
	relID string
}

func sheetList(data []byte) ([]sheetRef, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []sheetRef
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.StartElement:
			if e.Name.Local != "sheet" {
				continue
			}
			if len(out) >= maxSheets {
				return nil, fmt.Errorf("ooxml: the workbook lists more than %d sheets", maxSheets)
			}
			out = append(out, sheetRef{name: attr(e, "name"), relID: attrNS(e, "id")})
		case xml.EndElement:
			if e.Name.Local == "sheets" {
				return out, nil
			}
		}
	}
}

// sharedStrings reads xl/sharedStrings.xml into the table the cells index.
//
// An entry is <si> holding either one <t> or several <r><t>, the latter when
// the string is formatted in pieces; the pieces are one string and are joined
// without a separator.
func sharedStrings(data []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	var sb strings.Builder
	in := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "si":
				if len(out) >= maxSharedStr {
					return nil, fmt.Errorf("ooxml: more than %d shared strings", maxSharedStr)
				}
				in = true
				sb.Reset()
			case "t":
				if in {
					s, err := flatText(dec, e.Name)
					if err != nil {
						return nil, err
					}
					sb.WriteString(s)
				}
			case "rPh":
				// ⛔ A phonetic hint — the reading of a Japanese word, which a
				// spreadsheet stores beside the word and never shows in the
				// cell. Its <t> would otherwise be appended to the string.
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if e.Name.Local == "si" {
				out = append(out, sb.String())
				in = false
			}
		}
	}
}

// sheetRows reads a worksheet's cells into table rows.
//
// ⛔ A cell is placed by its REFERENCE, not by its position among its
// siblings. A row writes only the cells that hold something — <c r="A1"/>
// then <c r="C1"/> for a row whose B is empty — so a reader that appended in
// order would shift C into B's place and every column after it. Measured on no
// file: this one is from the specification, and it is the defect that would
// show up as "the numbers are in the wrong columns" on somebody's real
// spreadsheet rather than on any fixture.
func sheetRows(data []byte, shared []string) ([][]richdoc.Cell, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var rows [][]richdoc.Cell
	widest := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "row" {
			continue
		}
		if len(rows) >= maxRows {
			return nil, fmt.Errorf("ooxml: the sheet holds more than %d rows", maxRows)
		}
		cells, err := readRow(dec, se, shared)
		if err != nil {
			return nil, err
		}
		if len(cells) > widest {
			widest = len(cells)
		}
		rows = append(rows, cells)
	}
	// ⛔ Every row is padded to the widest, because a richdoc table is
	// rectangular and a consumer that walked a ragged one would read past the
	// end of a short row. The padding is at the END, where the missing cells
	// are.
	for i, r := range rows {
		for len(r) < widest {
			r = append(r, richdoc.Cell{})
		}
		rows[i] = r
	}
	// A sheet of nothing but empty rows is a sheet with no table, not a table
	// of nothing.
	if widest == 0 {
		return nil, nil
	}
	return rows, nil
}

func readRow(dec *xml.Decoder, se xml.StartElement, shared []string) ([]richdoc.Cell, error) {
	var cells []richdoc.Cell
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return cells, nil
			}
		case xml.StartElement:
			if e.Name.Local != "c" {
				if err := dec.Skip(); err != nil {
					return nil, err
				}
				continue
			}
			text, err := readCell(dec, e, shared)
			if err != nil {
				return nil, err
			}
			// Place it where its reference says, filling what the row left
			// out.
			if col, ok := columnOf(attr(e, "r")); ok {
				if col > maxColumns {
					return nil, fmt.Errorf("ooxml: a cell is at column %d, past the %d allowed", col, maxColumns)
				}
				for len(cells) < col-1 {
					cells = append(cells, richdoc.Cell{})
				}
			}
			cells = append(cells, richdoc.Cell{
				Inlines: []richdoc.Inline{richdoc.Text{Value: text}},
			})
		}
	}
}

// columnOf reads the column out of a cell reference: "A1" is 1, "B7" is 2,
// "AA3" is 27.
//
// ⛔ Base twenty-six with no zero: "Z" is 26 and "AA" is 27, not 26×1+1 = 27
// by accident — the digits run 1..26 rather than 0..25, so the usual
// accumulate-and-multiply is right only because A is 1. A reference with no
// letters, or one this does not understand, says so rather than guessing a
// column.
func columnOf(ref string) (int, bool) {
	n := 0
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		switch {
		case c >= 'A' && c <= 'Z':
			n = n*26 + int(c-'A') + 1
		case c >= 'a' && c <= 'z':
			n = n*26 + int(c-'a') + 1
		case c >= '0' && c <= '9':
			// The row number begins; the column is complete.
			return n, n > 0
		default:
			return 0, false
		}
		if n > maxColumns {
			return n, true // the caller refuses it, with the number
		}
	}
	return n, n > 0
}

// readCell reads one cell's value as text.
func readCell(dec *xml.Decoder, se xml.StartElement, shared []string) (string, error) {
	// ⛔ t is the cell's TYPE and it decides where the value is. Absent means
	// "n", a number, whose value is in <v> — Excel omits t for numbers while
	// LibreOffice writes t="n", so both have to work.
	typ := attr(se, "t")
	var v, inline strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return cellText(typ, v.String(), inline.String(), shared), nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "v":
				s, err := flatText(dec, e.Name)
				if err != nil {
					return "", err
				}
				v.WriteString(s)
			case "is":
				// An inline string: the text is here rather than in the
				// shared table.
				s, err := inlineString(dec, e)
				if err != nil {
					return "", err
				}
				inline.WriteString(s)
			case "f":
				// ⛔ A SIGNPOST, like word.go's instrText case, and a
				// surviving mutation established it here too: deleting this
				// case changes no test, because `default` below skips what
				// this reader does not name. The pair is defence in depth and
				// a mutation of one is masked by the other; the case stays
				// because it NAMES the formula, so its absence from the
				// output reads as deliberate rather than forgotten.
				//
				// ⛔ The FORMULA, not the value. "=SUM(A1:A9)" is what the
				// cell computes with and the number beside it in <v> is what
				// it showed, which is what a reader of the document wants.
				if err := dec.Skip(); err != nil {
					return "", err
				}
			default:
				if err := dec.Skip(); err != nil {
					return "", err
				}
			}
		}
	}
}

// cellText turns a cell's parts into the text it showed.
func cellText(typ, v, inline string, shared []string) string {
	switch typ {
	case "s":
		// The index into the shared table. ⛔ An index the table does not
		// hold yields NOTHING rather than the number: a file whose shared
		// strings are missing or truncated should come out with empty cells,
		// not with "0", "1", "2" in place of its words — which is exactly the
		// output a reader that ignored the type produces, and the hardest to
		// recognise as wrong.
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return shared[i]
	case "inlineStr":
		return inline
	case "b":
		// A boolean is stored as 0 or 1 and shown as FALSE or TRUE.
		if strings.TrimSpace(v) == "1" {
			return "TRUE"
		}
		return "FALSE"
	default:
		// "n" and "" are a number, "str" a formula's string result, "e" an
		// error like #DIV/0! — all of them already text in <v>.
		return v
	}
}

func inlineString(dec *xml.Decoder, se xml.StartElement) (string, error) {
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return sb.String(), nil
			}
		case xml.StartElement:
			if e.Name.Local == "t" {
				s, err := flatText(dec, e.Name)
				if err != nil {
					return "", err
				}
				sb.WriteString(s)
				continue
			}
			if err := dec.Skip(); err != nil {
				return "", err
			}
		}
	}
}
