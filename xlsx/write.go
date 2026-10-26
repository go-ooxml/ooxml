// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package xlsx

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/go-ooxml/ooxml/internal/xstring"
	"github.com/go-ooxml/opc"
)

// The parts a workbook is made of, and what they are called.
const (
	nsS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	nsR = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

	ctWorkbook = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
	ctSheet    = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"
	ctStyles   = "application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"
	ctShared   = "application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"

	relWorksheet = nsR + "/worksheet"
	relStyles    = nsR + "/styles"
	relShared    = nsR + "/sharedStrings"

	// firstCustomFormat is the first numFmtId a workbook may define for
	// itself: 0..163 are the built-in formats of §18.8.30, whose codes depend
	// on the reader's locale. Every format here is written out in full, so
	// that a date reads yyyy-mm-dd in every Excel rather than in the American
	// one.
	firstCustomFormat = 164
)

// maxUncompressedBytes is opc.Limits' default ceiling on all the parts
// together.
var maxUncompressedBytes = 384 << 20

// Write returns the workbook as a .xlsx package.
//
// The output is deterministic: no clock is read and nothing depends on map
// order, so the same workbook gives the same bytes.
func (w *Workbook) Write() ([]byte, error) {
	if len(w.sheets) == 0 {
		return nil, errors.New("xlsx: a workbook needs at least one sheet")
	}
	st := newStyles()
	sst := newShared()
	var parts []opc.NewPart
	var rels []opc.Rel
	total := 0
	add := func(name, ct string, data []byte) error {
		if len(data) > maxPartBytes {
			return fmt.Errorf("%w: %s would be %d bytes, past the %d a reader accepts", ErrTooLarge, name, len(data), maxPartBytes)
		}
		total += len(data)
		if total > maxUncompressedBytes {
			return fmt.Errorf("%w: the parts come to more than %d bytes", ErrTooLarge, maxUncompressedBytes)
		}
		parts = append(parts, opc.NewPart{Name: name, ContentType: ct, Data: data})
		return nil
	}
	for i, s := range w.sheets {
		data, err := s.xml(i == 0, st, sst)
		if err != nil {
			return nil, err
		}
		if err := add(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), ctSheet, data); err != nil {
			return nil, err
		}
		rels = append(rels, opc.Rel{
			ID: "rId" + strconv.Itoa(i+1), Type: relWorksheet,
			Target: fmt.Sprintf("worksheets/sheet%d.xml", i+1),
		})
	}
	next := len(w.sheets) + 1
	if err := add("xl/styles.xml", ctStyles, st.xml()); err != nil {
		return nil, err
	}
	rels = append(rels, opc.Rel{ID: "rId" + strconv.Itoa(next), Type: relStyles, Target: "styles.xml"})
	if len(sst.list) > 0 {
		if err := add("xl/sharedStrings.xml", ctShared, sst.xml()); err != nil {
			return nil, err
		}
		rels = append(rels, opc.Rel{ID: "rId" + strconv.Itoa(next+1), Type: relShared, Target: "sharedStrings.xml"})
	}
	if err := add("xl/workbook.xml", ctWorkbook, w.xml()); err != nil {
		return nil, err
	}
	out, err := opc.Build(parts, map[string][]opc.Rel{
		"":                {{ID: "rId1", Type: opc.RelOfficeDocument, Target: "xl/workbook.xml"}},
		"xl/workbook.xml": rels,
	})
	// ⛔ Build cannot fail here — every name above is one this file made — so
	// its error is passed on rather than given a branch no test can reach.
	if err == nil && len(out) > maxPackageBytes {
		return nil, fmt.Errorf("%w: the package would be %d bytes, past the %d a reader accepts", ErrTooLarge, len(out), maxPackageBytes)
	}
	return out, err
}

// xml writes xl/workbook.xml: one book view, and the sheets in order.
func (w *Workbook) xml() []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<workbook xmlns="%s" xmlns:r="%s"><bookViews><workbookView/></bookViews><sheets>`, nsS, nsR)
	for i, s := range w.sheets {
		b.WriteString(`<sheet name="`)
		esc(&b, s.name)
		fmt.Fprintf(&b, `" sheetId="%d" r:id="rId%d"/>`, i+1, i+1)
	}
	b.WriteString(`</sheets></workbook>`)
	return b.Bytes()
}

// xml writes one worksheet, in the element order CT_Worksheet's sequence
// fixes (sml.xsd): dimension, sheetViews, sheetFormatPr, cols, sheetData.
// A reader that validates refuses any other order.
func (s *Sheet) xml(first bool, st *styles, sst *shared) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<worksheet xmlns="%s" xmlns:r="%s">`, nsS, nsR)
	fmt.Fprintf(&b, `<dimension ref="%s"/>`, s.dimension())
	b.WriteString(`<sheetViews><sheetView workbookViewId="0"`)
	if first {
		b.WriteString(` tabSelected="1"`)
	}
	if s.header {
		// ⛔ A frozen pane is ySplit rows above topLeftCell, and the
		// selection must name the pane it is in: Excel repairs a sheet whose
		// selection is in a pane the view does not have.
		b.WriteString(`><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
			`<selection pane="bottomLeft" activeCell="A2" sqref="A2"/></sheetView>`)
	} else {
		b.WriteString(`/>`)
	}
	b.WriteString(`</sheetViews><sheetFormatPr defaultRowHeight="15"/>`)
	if len(s.widths) > 0 {
		cols := make([]int, 0, len(s.widths))
		for c := range s.widths {
			cols = append(cols, c)
		}
		slices.Sort(cols)
		b.WriteString(`<cols>`)
		for _, c := range cols {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%s" customWidth="1"/>`,
				c+1, c+1, strconv.FormatFloat(s.widths[c], 'f', -1, 64))
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)
	for r, row := range s.rows {
		// ⛔ An empty row is still written: a reader that walks the rows in
		// order, as the parent package's does, would otherwise move every
		// row below it up by one.
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		for c, cell := range row {
			if cell.kind == kindEmpty {
				continue
			}
			if err := writeCell(&b, columnName(c)+strconv.Itoa(r+1), cell, st, sst); err != nil {
				return nil, err
			}
		}
		b.WriteString(`</row>`)
		if b.Len() > maxPartBytes {
			return nil, fmt.Errorf("%w: sheet %q is past %d bytes", ErrTooLarge, s.name, maxPartBytes)
		}
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.Bytes(), nil
}

// dimension is the range the sheet's cells occupy, A1 for an empty one.
func (s *Sheet) dimension() string {
	last, wide := 0, 0
	for r, row := range s.rows {
		for c, cell := range row {
			if cell.kind != kindEmpty {
				last = r + 1
				wide = max(wide, c+1)
			}
		}
	}
	if last == 0 {
		return "A1"
	}
	return "A1:" + columnName(wide-1) + strconv.Itoa(last)
}

// writeCell writes one <c>.
func writeCell(b *bytes.Buffer, ref string, c Cell, st *styles, sst *shared) error {
	quote := c.kind == kindString && formulaLike(c.str)
	fmt.Fprintf(b, `<c r="%s"`, ref)
	if id := st.id(c.bold, c.format.code, quote); id != 0 {
		fmt.Fprintf(b, ` s="%d"`, id)
	}
	switch c.kind {
	case kindString:
		i, err := sst.index(c.str)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, ` t="s"><v>%d</v></c>`, i)
	case kindBool:
		fmt.Fprintf(b, ` t="b"><v>%d</v></c>`, int(c.num))
	default: // a number, or a date's serial
		fmt.Fprintf(b, `><v>%s</v></c>`, number(c.num))
	}
	return nil
}

// number spells a double the way <v> holds it: the shortest decimal that
// reads back as the same double.
func number(v float64) string {
	if v == 0 {
		return "0" // and not "-0"
	}
	// Plain digits where they stay short — 'G' would write 1000000 as
	// 1E+06 — and an exponent at the extremes, where plain digits would run
	// to hundreds of zeros.
	if a := math.Abs(v); a >= 1e-7 && a < 1e21 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'E', -1, 64)
}

// formulaLike says whether a person editing this text in a spreadsheet would
// turn it into a formula by confirming it: = + - and @ begin one.
func formulaLike(s string) bool {
	return s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@')
}

// shared is the shared strings table (CT_Sst): every distinct text once, and
// cells holding its index.
//
// Shared rather than inline strings, though inline ones would be simpler:
// the shared table is what Excel itself writes, so it is the path every
// reader of .xlsx has been tested against, and a report that repeats a label
// a thousand times stores it once.
type shared struct {
	list []string
	pos  map[string]int
	refs int
}

func newShared() *shared { return &shared{pos: map[string]int{}} }

func (t *shared) index(s string) (int, error) {
	t.refs++
	if i, ok := t.pos[s]; ok {
		return i, nil
	}
	if len(t.list) >= maxSharedStrings {
		return 0, fmt.Errorf("%w: more than %d distinct texts", ErrTooLarge, maxSharedStrings)
	}
	t.pos[s] = len(t.list)
	t.list = append(t.list, s)
	return len(t.list) - 1, nil
}

func (t *shared) xml() []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<sst xmlns="%s" count="%d" uniqueCount="%d">`, nsS, t.refs, len(t.list))
	for _, s := range t.list {
		// ⛔ xml:space="preserve": without it a reader that follows XML
		// trims "  indented" to "indented".
		b.WriteString(`<si><t xml:space="preserve">`)
		esc(&b, xstring.Encode(s))
		b.WriteString(`</t></si>`)
	}
	b.WriteString(`</sst>`)
	return b.Bytes()
}

// styles is the stylesheet (CT_Stylesheet): the cell formats the cells point at by
// index, each a combination of a font, a number format and quotePrefix.
type styles struct {
	keys    []styleKey
	byKey   map[styleKey]int
	formats []string
	fmtID   map[string]int
}

type styleKey struct {
	bold  bool
	code  string
	quote bool
}

func newStyles() *styles {
	s := &styles{byKey: map[styleKey]int{}, fmtID: map[string]int{}}
	s.id(false, "", false) // index 0, the default every unstyled cell has
	return s
}

func (s *styles) id(bold bool, code string, quote bool) int {
	k := styleKey{bold, code, quote}
	if i, ok := s.byKey[k]; ok {
		return i
	}
	if code != "" {
		if _, ok := s.fmtID[code]; !ok {
			s.fmtID[code] = firstCustomFormat + len(s.formats)
			s.formats = append(s.formats, code)
		}
	}
	s.byKey[k] = len(s.keys)
	s.keys = append(s.keys, k)
	return len(s.keys) - 1
}

// xml writes xl/styles.xml, in CT_Stylesheet's order.
//
// ⛔ Two fills, not one: none and gray125. The schema asks for neither, but
// Excel and openpyxl both begin every stylesheet with that pair and treat the
// first two fill indexes as reserved, so a workbook whose cells' fill 0 or 1
// is anything else is not what those readers expect to find.
func (s *styles) xml() []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<styleSheet xmlns="%s">`, nsS)
	if len(s.formats) > 0 {
		fmt.Fprintf(&b, `<numFmts count="%d">`, len(s.formats))
		for i, code := range s.formats {
			fmt.Fprintf(&b, `<numFmt numFmtId="%d" formatCode="`, firstCustomFormat+i)
			esc(&b, code)
			b.WriteString(`"/>`)
		}
		b.WriteString(`</numFmts>`)
	}
	b.WriteString(`<fonts count="2">` +
		`<font><sz val="11"/><name val="Calibri"/><family val="2"/></font>` +
		`<font><b/><sz val="11"/><name val="Calibri"/><family val="2"/></font>` +
		`</fonts>` +
		`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`)
	fmt.Fprintf(&b, `<cellXfs count="%d">`, len(s.keys))
	for _, k := range s.keys {
		num, font := 0, 0
		if k.code != "" {
			num = s.fmtID[k.code]
		}
		if k.bold {
			font = 1
		}
		fmt.Fprintf(&b, `<xf numFmtId="%d" fontId="%d" fillId="0" borderId="0" xfId="0"`, num, font)
		if num != 0 {
			b.WriteString(` applyNumberFormat="1"`)
		}
		if font != 0 {
			b.WriteString(` applyFont="1"`)
		}
		if k.quote {
			b.WriteString(` quotePrefix="1"`)
		}
		b.WriteString(`/>`)
	}
	b.WriteString(`</cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`)
	return b.Bytes()
}

// esc appends text escaped for XML. A bytes.Buffer never fails a write, so
// the error xml.EscapeText passes through cannot happen.
func esc(b *bytes.Buffer, s string) { _ = xml.EscapeText(b, []byte(s)) }
