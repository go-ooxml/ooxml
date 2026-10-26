// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package xlsx writes SpreadsheetML workbooks (.xlsx) from typed cells.
//
//	wb := xlsx.New()
//	sh, _ := wb.AddSheet("Costs")
//	sh.SetHeader("Model", "Tokens", "Cost (EUR)", "Share", "Day")
//	sh.SetColumnWidth(0, 28)
//	sh.AppendRow(xlsx.String("opus"), xlsx.Number(1.2e6),
//		xlsx.Number(18.5).Format(xlsx.Decimal(2)),
//		xlsx.Number(0.42).Format(xlsx.Percent(1)),
//		xlsx.Date(day))
//	data, err := wb.Write()
//
// The parent package [github.com/go-ooxml/ooxml] turns a richdoc document's
// tables into a workbook, where every cell is text. This one is for a program
// that HAS types: a number is written as a number, so a spreadsheet can sum
// it, and a date as a date serial with a date format, so it sorts as one.
//
// ⛔ No formula is ever written. There is no API for one, and a string is
// always a string: "=SUM(A1:A9)", "+1", "-1" and "@x" are stored as shared
// strings, and their cells carry quotePrefix so that a person who edits one
// in Excel does not turn it into a formula by pressing Enter.
//
// Every limit Excel documents is enforced when the cell is added, not when
// the file is opened somewhere else: 1 048 576 rows, 16 384 columns, 32 767
// characters a cell, 31 characters a sheet name, 255 a column width.
package xlsx

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// The limits, from Excel's "specifications and limits" and from what the
// reader in the parent package accepts. They are var so that a test can lower
// them: a test that had to build a million rows to see a refusal would be a
// test nobody runs.
var (
	maxRows        = 1 << 20 // 1 048 576
	maxColumns     = 1 << 14 // 16 384, XFD
	maxCellChars   = 32767   // counted in UTF-16 units, as Excel counts them
	maxSheets      = 1024
	maxSheetName   = 31
	maxColumnWidth = 255.0
	// maxPartBytes keeps every part under opc.Limits' default ceiling, so a
	// workbook written here is one ooxml.ParseWorkbook reads with the
	// defaults.
	maxPartBytes = 64 << 20
	// maxPackageBytes is the same for the whole file.
	maxPackageBytes = 256 << 20
	// maxSharedStrings is the parent package's reader's ceiling.
	maxSharedStrings = 1 << 21
)

// ErrTooLarge is wrapped by every refusal that is about size.
var ErrTooLarge = errors.New("xlsx: too large")

// A Workbook is a set of sheets, written by [Workbook.Write].
type Workbook struct {
	sheets []*Sheet
	names  map[string]bool // folded, as Excel compares them
}

// New returns an empty workbook. A workbook needs at least one sheet before
// it can be written.
func New() *Workbook { return &Workbook{names: map[string]bool{}} }

// A Sheet is one worksheet: a header, rows of cells, column widths.
type Sheet struct {
	name   string
	rows   [][]Cell
	widths map[int]float64
	header bool
}

// AddSheet appends a sheet.
//
// ⛔ The name is refused rather than repaired. Excel refuses the WHOLE FILE
// over a name longer than 31 characters, one holding : \ / ? * [ ], one that
// begins or ends with an apostrophe, the reserved "History", or a second sheet
// whose name differs only in case — and a writer that quietly renamed would
// hand back a workbook whose sheet is not the one the caller asked for.
func (w *Workbook) AddSheet(name string) (*Sheet, error) {
	if err := checkSheetName(name); err != nil {
		return nil, err
	}
	key := strings.ToLower(name)
	if w.names[key] {
		return nil, fmt.Errorf("xlsx: there is already a sheet called %q (Excel ignores case)", name)
	}
	if len(w.sheets) >= maxSheets {
		return nil, fmt.Errorf("%w: more than %d sheets", ErrTooLarge, maxSheets)
	}
	w.names[key] = true
	s := &Sheet{name: name, widths: map[int]float64{}}
	w.sheets = append(w.sheets, s)
	return s, nil
}

func checkSheetName(name string) error {
	switch {
	case name == "":
		return errors.New("xlsx: a sheet needs a name")
	case !utf8.ValidString(name):
		return fmt.Errorf("xlsx: sheet name %q is not UTF-8", name)
	case utf16Len(name) > maxSheetName:
		return fmt.Errorf("xlsx: sheet name %q is longer than %d characters", name, maxSheetName)
	case strings.ContainsAny(name, `:\/?*[]`):
		return fmt.Errorf(`xlsx: sheet name %q holds one of : \ / ? * [ ]`, name)
	case name[0] == '\'' || name[len(name)-1] == '\'':
		return fmt.Errorf("xlsx: sheet name %q begins or ends with an apostrophe", name)
	case strings.EqualFold(name, "History"):
		return errors.New("xlsx: Excel reserves the sheet name History")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7F || r == 0xFFFE || r == 0xFFFF {
			return fmt.Errorf("xlsx: sheet name %q holds a control character", name)
		}
	}
	return nil
}

// Name is the sheet's name.
func (s *Sheet) Name() string { return s.name }

// SetHeader writes the first row: the titles, in bold, with the pane frozen
// below them so that they stay in view as the sheet scrolls.
//
// It must come before any other row.
func (s *Sheet) SetHeader(titles ...string) error {
	if len(s.rows) > 0 {
		return errors.New("xlsx: the header must be the first row")
	}
	cells := make([]Cell, len(titles))
	for i, t := range titles {
		cells[i] = String(t).Bold()
	}
	if err := s.AppendRow(cells...); err != nil {
		return err
	}
	s.header = true
	return nil
}

// AppendRow adds a row below the last one. An empty [Cell] (the zero value)
// leaves its place blank.
func (s *Sheet) AppendRow(cells ...Cell) error {
	if len(s.rows) >= maxRows {
		return fmt.Errorf("%w: sheet %q already holds %d rows", ErrTooLarge, s.name, maxRows)
	}
	if len(cells) > maxColumns {
		return fmt.Errorf("%w: a row of %d cells, past the %d columns a sheet has", ErrTooLarge, len(cells), maxColumns)
	}
	row := make([]Cell, len(cells))
	for i, c := range cells {
		c, err := c.prepare()
		if err != nil {
			return fmt.Errorf("xlsx: sheet %q, row %d, column %s: %w", s.name, len(s.rows)+1, columnName(i), err)
		}
		row[i] = c
	}
	s.rows = append(s.rows, row)
	return nil
}

// SetColumnWidth sets a column's width, in characters of the default font as
// SpreadsheetML measures it. col counts from 0, which is column A.
func (s *Sheet) SetColumnWidth(col int, width float64) error {
	if col < 0 || col >= maxColumns {
		return fmt.Errorf("xlsx: column %d is outside 0..%d", col, maxColumns-1)
	}
	if !(width >= 0 && width <= maxColumnWidth) {
		return fmt.Errorf("xlsx: a column width of %v is outside 0..%v", width, maxColumnWidth)
	}
	s.widths[col] = width
	return nil
}

type kind uint8

const (
	kindEmpty kind = iota
	kindNumber
	kindString
	kindBool
	kindDate
)

// A Cell is one typed value. The zero value is an empty cell.
type Cell struct {
	kind   kind
	num    float64
	str    string
	when   time.Time
	format Format
	bold   bool
}

// Number is a numeric cell. NaN and ±Inf are refused when the row is added:
// a spreadsheet has no way to store either.
func Number(v float64) Cell { return Cell{kind: kindNumber, num: v} }

// String is a text cell. It is never a formula, whatever it begins with.
func String(s string) Cell { return Cell{kind: kindString, str: s} }

// Bool is a TRUE or FALSE cell.
func Bool(v bool) Cell {
	c := Cell{kind: kindBool}
	if v {
		c.num = 1
	}
	return c
}

// Date is a date cell, shown as yyyy-mm-dd. Its time of day is kept in the
// value; [DateTimeFormat] is the format that shows it.
//
// The date is read on t's own wall clock: 2026-10-26 23:30 in Paris is the
// 26th, whatever the time is in UTC. A spreadsheet has no time zones.
func Date(t time.Time) Cell { return Cell{kind: kindDate, when: t, format: DateFormat} }

// Format sets how a number or a date is shown.
func (c Cell) Format(f Format) Cell { c.format = f; return c }

// Bold sets the cell's font to bold.
func (c Cell) Bold() Cell { c.bold = true; return c }

// prepare checks a cell against what a workbook can hold and turns a date
// into its serial.
func (c Cell) prepare() (Cell, error) {
	switch c.kind {
	case kindNumber:
		if math.IsNaN(c.num) || math.IsInf(c.num, 0) {
			return c, fmt.Errorf("%v is not a number a spreadsheet can hold", c.num)
		}
	case kindString:
		if n := utf16Len(c.str); n > maxCellChars {
			return c, fmt.Errorf("%w: a text of %d characters, past the %d a cell holds", ErrTooLarge, n, maxCellChars)
		}
	case kindDate:
		serial, err := dateSerial(c.when)
		if err != nil {
			return c, err
		}
		c.num = serial
	}
	return c, nil
}

// The range of dates written. ⛔ Not the standard's whole range: ECMA-376
// Part 1 §18.17.4.1 counts the 1900 date system from 1899-12-30, but Excel
// counts 1900 as a leap year and so disagrees with it about every day before
// 1900-03-01 — the same file shows a different date in Excel and in a reader
// that follows the standard. From 1900-03-01 on the two agree, so that is
// where this starts; and Excel shows no date past 9999-12-31.
var (
	firstDate = time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC)
	lastDate  = time.Date(9999, 12, 31, 23, 59, 59, 999e6, time.UTC)
	epoch     = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
)

// dateSerial is §18.17.4.1's serial date-time: days since 1899-12-30, the
// time of day as the fraction (§18.17.4.2).
func dateSerial(t time.Time) (float64, error) {
	y, m, d := t.Date()
	h, mi, s := t.Clock()
	wall := time.Date(y, m, d, h, mi, s, t.Nanosecond(), time.UTC)
	if wall.Before(firstDate) || wall.After(lastDate) {
		return 0, fmt.Errorf("the date %s is outside %s..%s, the range Excel and the standard agree on",
			wall.Format("2006-01-02"), firstDate.Format("2006-01-02"), lastDate.Format("2006-01-02"))
	}
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	// ⛔ In seconds, not through Time.Sub: a Duration holds 292 years, and
	// Sub saturates rather than failing — 9999-12-31 would come out as a day
	// in 2192.
	days := float64((day.Unix() - epoch.Unix()) / 86400)
	frac := float64(wall.Sub(day)) / float64(24*time.Hour)
	return days + frac, nil
}

// A Format is a number format. The zero value is General.
//
// The codes are built here rather than taken from the caller: a format code is
// a small language of its own (§18.8.31), and the ones a report needs are few.
type Format struct{ code string }

// The formats with no parameter.
var (
	General        = Format{}
	DateFormat     = Format{"yyyy-mm-dd"}
	DateTimeFormat = Format{"yyyy-mm-dd hh:mm:ss"}
)

// maxPlaces bounds the decimals a format may ask for. A double holds about 15
// significant digits, so more would show noise.
const maxPlaces = 15

// Decimal shows a number with a thousands separator and places decimals:
// Decimal(2) is #,##0.00. places is clamped to 0..15.
func Decimal(places int) Format { return Format{"#,##0" + fraction(places)} }

// Percent shows a fraction as a percentage: Percent(1) shows 0.425 as 42.5%.
// places is clamped to 0..15.
func Percent(places int) Format { return Format{"0" + fraction(places) + "%"} }

func fraction(places int) string {
	places = max(0, min(places, maxPlaces))
	if places == 0 {
		return ""
	}
	return "." + strings.Repeat("0", places)
}

// utf16Len is how many UTF-16 units s takes, which is how Excel counts the
// characters in a cell.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// columnName is a column's letters: 0 is A, 25 Z, 26 AA.
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
