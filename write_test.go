// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// ⛔ THE CONTROL FOR A WRITER IS A READER. What a writer owes is not a
// particular string of XML — that is only what its author believed — but a
// file something else can read. So almost everything below writes a document
// and reads it back with ParseWord or ParseWorkbook, which were here first and
// were measured against files Word and LibreOffice produced.
//
// What that cannot show is whether WORD opens it, and nothing here can run
// Word. Where that is the only question, the test says so and asserts the
// spelling the standard requires instead.

// sample is a document with one of everything this writes.
func sample() *richdoc.Document {
	return &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Heading{Level: 1, Inlines: []richdoc.Inline{richdoc.Txt("The Title")}},
		richdoc.Paragraph{Inlines: []richdoc.Inline{
			richdoc.Txt("a paragraph with "),
			richdoc.Bold(richdoc.Txt("bold")),
			richdoc.Txt(" and "),
			richdoc.Italic(richdoc.Txt("italic")),
			richdoc.Txt(" in it"),
		}},
		richdoc.Heading{Level: 2, Inlines: []richdoc.Inline{richdoc.Txt("A Section")}},
		richdoc.List{Tight: true, Items: []richdoc.ListItem{
			{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("one")}}}},
			{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("two")}}}},
		}},
		richdoc.Table{
			Header: cellsOf("Name", "Age"),
			Rows:   [][]richdoc.Cell{cellsOf("Ada", "36"), cellsOf("Bob", "41")},
		},
	}}
}

func cellsOf(texts ...string) []richdoc.Cell {
	out := make([]richdoc.Cell, len(texts))
	for i, s := range texts {
		out[i] = richdoc.Cell{Inlines: []richdoc.Inline{richdoc.Txt(s)}}
	}
	return out
}

func TestWhatIsWrittenCanBeRead(t *testing.T) {
	b, err := WriteWord(sample())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatalf("what was written could not be read: %v", err)
	}
	text := richdoc.PlainText(got)
	for _, want := range []string{
		"The Title", "a paragraph with bold and italic in it", "A Section",
		"one", "two", "Name", "Age", "Ada", "36", "Bob", "41",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not in what came back:\n%s", want, text)
		}
	}
}

// The headings come back as headings, at the levels they went in at — which
// is the whole reason a styles part is written.
func TestTheHeadingsComeBackAsHeadings(t *testing.T) {
	b, err := WriteWord(sample())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	levels := map[string]int{}
	for _, blk := range got.Blocks {
		if h, ok := blk.(richdoc.Heading); ok {
			levels[richdoc.PlainText(&richdoc.Document{Blocks: []richdoc.Block{h}})] = h.Level
		}
	}
	if levels["The Title"] != 1 {
		t.Errorf("the title came back at level %d", levels["The Title"])
	}
	if levels["A Section"] != 2 {
		t.Errorf("the section came back at level %d", levels["A Section"])
	}
}

// The table comes back as a table, with its rows and its cells.
func TestTheTableComesBackAsATable(t *testing.T) {
	b, err := WriteWord(sample())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var table *richdoc.Table
	for _, blk := range got.Blocks {
		if tb, ok := blk.(richdoc.Table); ok {
			table = &tb
			break
		}
	}
	if table == nil {
		t.Fatalf("no table came back: %s", richdoc.PlainText(got))
	}
	// Three rows went in — a header and two — and the reader may put the
	// header in Header or in the first row; either way there are three.
	rows := len(table.Rows)
	if len(table.Header) > 0 {
		rows++
	}
	if rows != 3 {
		t.Errorf("%d rows came back, want 3: %+v", rows, table)
	}
}

// ⛔ A run's leading and trailing spaces survive. Without xml:space="preserve"
// a document rebuilt from runs that each dropped their edges reads as
// onewordafteranother — and the sample above is exactly that shape, three
// runs whose spaces are at their ends.
func TestARunsSpacesSurvive(t *testing.T) {
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Paragraph{Inlines: []richdoc.Inline{
			richdoc.Txt("before "), richdoc.Bold(richdoc.Txt("middle")), richdoc.Txt(" after"),
		}},
	}}
	b, err := WriteWord(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if text := richdoc.PlainText(got); !strings.Contains(text, "before middle after") {
		t.Errorf("the spaces were lost: %q", text)
	}
}

// A document with nothing in it is still a document: Word wants a paragraph,
// and a reader that finds none has nothing to put a cursor in.
func TestADocumentWithNothingInIt(t *testing.T) {
	b, err := WriteWord(&richdoc.Document{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseWord(b, opc.Limits{}); err != nil {
		t.Fatalf("an empty document could not be read: %v", err)
	}
	if !strings.Contains(string(bodyOf(t, b)), "<w:p/>") {
		t.Error("the body has no paragraph in it")
	}
}

func TestNoDocumentAtAll(t *testing.T) {
	if _, err := WriteWord(nil); err == nil {
		t.Error("nil was written")
	}
	if _, err := WriteWorkbook(nil); err == nil {
		t.Error("nil was written as a workbook")
	}
}

// ⛔ The numbering part is there when there is a list and NOT otherwise. A
// w:numId naming a w:num that is not in the package is a list Word shows
// without its bullets; a numbering part nothing refers to is bytes.
func TestTheNumberingPartIsThereExactlyWhenNeeded(t *testing.T) {
	withList, err := WriteWord(sample())
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(withList, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has("word/numbering.xml") {
		t.Errorf("a document with a list has no numbering part: %v", p.Parts())
	}
	rel, ok, err := p.RelOfType("word/document.xml", relNumbering)
	if err != nil || !ok {
		t.Fatalf("nothing points at the numbering part: %v, %v", ok, err)
	}
	if target, err := p.Resolve("word/document.xml", rel); err != nil || target != "word/numbering.xml" {
		t.Errorf("the numbering relationship resolves to %q, %v", target, err)
	}

	plain, err := WriteWord(&richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("no list here")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := opc.Open(plain, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if p2.Has("word/numbering.xml") {
		t.Errorf("a document with no list carries a numbering part: %v", p2.Parts())
	}
}

// ⛔ The heading styles are DEFINED, not left to Word's latent ones. A
// w:pStyle naming a style the package does not define works in Word, which
// knows "Heading1" already, and shows as plain text everywhere else. This has
// to assert the spelling: our own reader reads the style NAME and would be
// just as happy without the definition.
func TestTheHeadingStylesAreDefined(t *testing.T) {
	b, err := WriteWord(sample())
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rel, ok, err := p.RelOfType("word/document.xml", relStyles)
	if err != nil || !ok {
		t.Fatalf("nothing points at a styles part: %v, %v", ok, err)
	}
	target, err := p.Resolve("word/document.xml", rel)
	if err != nil {
		t.Fatal(err)
	}
	styles, err := p.Part(target)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		want := `w:styleId="Heading` + string(rune('0'+i)) + `"`
		if !strings.Contains(string(styles), want) {
			t.Errorf("%s is not defined:\n%s", want, styles)
		}
	}
	if !strings.Contains(string(styles), `w:styleId="ListParagraph"`) {
		t.Error("the list style is not defined")
	}
}

// A heading deeper than the levels there are is written AS the last one, not
// dropped: text somebody made a heading is a heading whatever depth they gave
// it.
func TestAHeadingDeeperThanTheLevelsThereAre(t *testing.T) {
	b, err := WriteWord(&richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Heading{Level: 9, Inlines: []richdoc.Inline{richdoc.Txt("deep")}},
		richdoc.Heading{Level: 0, Inlines: []richdoc.Inline{richdoc.Txt("shallow")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyOf(t, b))
	if !strings.Contains(body, `w:val="Heading6"`) {
		t.Errorf("a level nine heading is not at the last level:\n%s", body)
	}
	if !strings.Contains(body, `w:val="Heading1"`) {
		t.Errorf("a level zero heading is not at the first level:\n%s", body)
	}
}

// Blocks richdoc has that WordprocessingML has no shape for keep their WORDS.
// Dropping them loses the author's text for the sake of a shape Word cannot
// draw.
func TestABlockWordHasNoShapeFor(t *testing.T) {
	b, err := WriteWord(&richdoc.Document{Blocks: []richdoc.Block{
		richdoc.CodeBlock{Language: "go", Text: "one\ntwo\n"},
		richdoc.BlockQuote{Blocks: []richdoc.Block{
			richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("quoted")}},
		}},
		richdoc.MathBlock{TeX: `x^2`},
		richdoc.ThematicBreak{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	text := richdoc.PlainText(got)
	for _, want := range []string{"one", "two", "quoted"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q was dropped:\n%s", want, text)
		}
	}
	// A code block's lines stay separate lines, which is why a paragraph
	// each: one w:br would make them a single line that wraps.
	if strings.Contains(text, "onetwo") || strings.Contains(text, "one two") {
		t.Errorf("the code block's lines ran together:\n%s", text)
	}
}

// --- the workbook -------------------------------------------------------

func TestWhatIsWrittenAsAWorkbookCanBeRead(t *testing.T) {
	b, err := WriteWorkbook(sample())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWorkbook(b, opc.Limits{})
	if err != nil {
		t.Fatalf("what was written could not be read: %v", err)
	}
	text := richdoc.PlainText(got)
	for _, want := range []string{"Name", "Age", "Ada", "36", "Bob", "41"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not in what came back:\n%s", want, text)
		}
	}
	// ⛔ And the document's prose is NOT there. A spreadsheet is a grid of
	// cells, and putting the headings in column A would make a file that
	// looks like a document and works as neither.
	if strings.Contains(text, "The Title") {
		t.Errorf("the document's prose went into the sheet:\n%s", text)
	}
}

// One sheet per table, and the names are legal: Excel refuses a name over 31
// characters or a duplicate, and it refuses the whole FILE with it.
func TestOneSheetPerTable(t *testing.T) {
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Table{Header: cellsOf("a"), Rows: [][]richdoc.Cell{cellsOf("1")}},
		richdoc.Table{Header: cellsOf("b"), Rows: [][]richdoc.Cell{cellsOf("2")}},
		richdoc.Table{Header: cellsOf("c"), Rows: [][]richdoc.Cell{cellsOf("3")}},
	}}
	b, err := WriteWorkbook(d)
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		name := "xl/worksheets/sheet" + string(rune('0'+i)) + ".xml"
		if !p.Has(name) {
			t.Errorf("%s is missing: %v", name, p.Parts())
		}
	}
	wb, err := p.Part("xl/workbook.xml")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		want := `name="Table ` + string(rune('0'+i)) + `"`
		if !strings.Contains(string(wb), want) {
			t.Errorf("%s is not in the workbook:\n%s", want, wb)
		}
		if n := strings.Count(string(wb), want); n != 1 {
			t.Errorf("%s appears %d times", want, n)
		}
	}
}

// A table inside a list item or a quotation is still a table, and a walk that
// only looked at the top level would miss it.
func TestATableInsideSomethingElse(t *testing.T) {
	inner := richdoc.Table{Header: cellsOf("buried"), Rows: [][]richdoc.Cell{cellsOf("deep")}}
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.BlockQuote{Blocks: []richdoc.Block{
			richdoc.List{Items: []richdoc.ListItem{{Blocks: []richdoc.Block{inner}}}},
		}},
	}}
	b, err := WriteWorkbook(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWorkbook(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if text := richdoc.PlainText(got); !strings.Contains(text, "buried") {
		t.Errorf("a table inside a quotation and a list was missed: %q", text)
	}
}

// A table inside a CELL of another table, which is rare and real.
func TestATableInsideACell(t *testing.T) {
	inner := richdoc.Table{Header: cellsOf("nested"), Rows: [][]richdoc.Cell{cellsOf("x")}}
	outer := richdoc.Table{
		Header: cellsOf("outer"),
		Rows: [][]richdoc.Cell{{
			// Blocks AND Inlines, as richdoc.Cell says a producer must do.
			{Inlines: []richdoc.Inline{richdoc.Txt("nested x")},
				Blocks: []richdoc.Block{inner}},
		}},
	}
	b, err := WriteWorkbook(&richdoc.Document{Blocks: []richdoc.Block{outer}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has("xl/worksheets/sheet2.xml") {
		t.Errorf("the nested table got no sheet of its own: %v", p.Parts())
	}
}

// A document with no tables gives a workbook with one empty sheet: "there was
// nothing to put in a grid" is an answer, and a workbook with no sheets at all
// is a file Excel refuses.
func TestADocumentWithNoTables(t *testing.T) {
	b, err := WriteWorkbook(&richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("prose only")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has("xl/worksheets/sheet1.xml") {
		t.Errorf("no sheet at all: %v", p.Parts())
	}
	if _, err := ParseWorkbook(b, opc.Limits{}); err != nil {
		t.Errorf("an empty workbook could not be read: %v", err)
	}
}

// An empty cell is left out of the sheet, because a sheet says where each cell
// is and an empty one carries nothing. The cells after it keep their places,
// which is the thing that would break if they were simply shifted along.
func TestAnEmptyCellKeepsTheOnesAfterItInPlace(t *testing.T) {
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Table{
			Header: cellsOf("a", "b", "c"),
			Rows:   [][]richdoc.Cell{{{}, {}, {Inlines: []richdoc.Inline{richdoc.Txt("third")}}}},
		},
	}}
	b, err := WriteWorkbook(d)
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := p.Part("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sheet), `r="C2"`) {
		t.Errorf("the third cell is not at C2:\n%s", sheet)
	}
	if strings.Contains(string(sheet), `r="A2"`) {
		t.Errorf("an empty cell was written:\n%s", sheet)
	}
}

// ⛔ A cell's text is an ST_Xstring (§22.9.2.19) both ways: a control
// character is spelled _x0001_ on the way out and decoded on the way in, and
// text that merely looks like such a spelling survives as itself.
func TestTextThatLooksLikeAnEscapeSurvives(t *testing.T) {
	const text = "_x0041_ then \x01"
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Table{Rows: [][]richdoc.Cell{{{Inlines: []richdoc.Inline{richdoc.Txt(text)}}}}},
	}}
	b, err := WriteWorkbook(d)
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := p.Part("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sheet), `_x005F_x0041_ then _x0001_`) {
		t.Errorf("not escaped:\n%s", sheet)
	}
	back, err := ParseWorkbook(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got := back.Blocks[1].(richdoc.Table).Rows[0][0].Inlines[0].(richdoc.Text).Value
	if got != text {
		t.Errorf("read back %q, want %q", got, text)
	}
}

// ⛔ The spreadsheet column spelling, which is NOT the one PDF page labels
// use: the 27th is AA in both and the 28th is AB here and BB there. The two
// look alike exactly where somebody would check and part company one step
// later.
func TestTheColumnLetters(t *testing.T) {
	cases := map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"}
	for n, want := range cases {
		if got := columnName(n); got != want {
			t.Errorf("column %d is %q, want %q", n, got, want)
		}
	}
}

// A sheet name that collides is made unique, and stays within Excel's limit.
func TestASheetNameThatCollides(t *testing.T) {
	taken := map[string]bool{}
	first := sheetName(0, taken)
	second := sheetName(0, taken)
	if first == second {
		t.Errorf("two sheets are both called %q", first)
	}
	for _, name := range []string{first, second} {
		if len(name) > sheetNameLimit {
			t.Errorf("%q is %d characters, over the limit of %d", name, len(name), sheetNameLimit)
		}
	}
}

// bodyOf reads word/document.xml back out of a written package.
func bodyOf(t *testing.T, b []byte) []byte {
	t.Helper()
	p, err := opc.Open(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	main, err := p.Main()
	if err != nil {
		t.Fatal(err)
	}
	data, err := p.Part(main)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A list item is not always one paragraph: it can hold several blocks, a
// nested list, or nothing at all. The bullet goes on the FIRST paragraph, so
// that a wrapped item is one bullet and not two.
func TestListItemsThatAreNotOneParagraph(t *testing.T) {
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.List{Items: []richdoc.ListItem{
			{Blocks: []richdoc.Block{
				richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("first")}},
				richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("and more of it")}},
			}},
			{Blocks: []richdoc.Block{
				richdoc.List{Items: []richdoc.ListItem{
					{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Txt("nested")}}}},
				}},
			}},
			{}, // an item with nothing in it is still an item
		}},
	}}
	b, err := WriteWord(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	text := richdoc.PlainText(got)
	for _, want := range []string{"first", "and more of it", "nested"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q was lost:\n%s", want, text)
		}
	}
	body := string(bodyOf(t, b))
	// Three marked items: "first", the nested one, and the empty one. The
	// SECOND paragraph of the first item carries no bullet, which is the
	// property that matters — a wrapped item is one bullet, not two.
	if n := strings.Count(body, "<w:numPr>"); n != 3 {
		t.Errorf("%d bullets, want 3:\n%s", n, body)
	}
	rest := body[strings.Index(body, "and more of it")-80:]
	if i := strings.Index(rest, "and more of it"); strings.Contains(rest[:i], "<w:numPr>") {
		t.Errorf("the second paragraph of an item carries a bullet:\n%s", body)
	}
	// And the nested list is one level in, which is what a nested bullet is.
	if !strings.Contains(body, `<w:ilvl w:val="1"/>`) {
		t.Errorf("the nested list is not indented:\n%s", body)
	}
}

// Inline code and an inline with nothing in it.
func TestInlineCodeAndAnEmptyInline(t *testing.T) {
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Paragraph{Inlines: []richdoc.Inline{
			richdoc.Txt("call "), richdoc.Mono("Parse()"), richdoc.Txt(""),
			richdoc.Bold(), // Strong with no inlines at all
		}},
	}}
	b, err := WriteWord(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if text := richdoc.PlainText(got); !strings.Contains(text, "call Parse()") {
		t.Errorf("read %q", text)
	}
	// An empty run is not written: `<w:r><w:t/></w:r>` is a run that says
	// nothing, and a reader counting runs would count it.
	if body := string(bodyOf(t, b)); strings.Contains(body, `<w:t xml:space="preserve"></w:t>`) {
		t.Errorf("an empty run was written:\n%s", body)
	}
}

// A table with no rows at all writes nothing: `<w:tbl>` with no `<w:tr>` is
// what makes Word call a document corrupt.
func TestATableWithNoRows(t *testing.T) {
	b, err := WriteWord(&richdoc.Document{Blocks: []richdoc.Block{richdoc.Table{}}})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(bodyOf(t, b)); strings.Contains(body, "<w:tbl>") {
		t.Errorf("an empty table was written:\n%s", body)
	}
	if _, err := ParseWord(b, opc.Limits{}); err != nil {
		t.Fatalf("it could not be read: %v", err)
	}
}

// ⛔ Every row needs the same number of cells, and every cell needs a
// paragraph. A short row, an empty cell and a cell holding a block that
// writes nothing are the three ways to end up with neither.
func TestARaggedTable(t *testing.T) {
	d := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Table{
			Header: cellsOf("a", "b", "c"),
			Rows: [][]richdoc.Cell{
				cellsOf("short"), // one cell where the header has three
				{{}, {Inlines: []richdoc.Inline{richdoc.Txt("mid")}}, {}},
				// A cell whose content is a list with no items: blocks that
				// write no paragraph at all.
				{{Blocks: []richdoc.Block{richdoc.List{}}}, {}, {}},
			},
		},
	}}
	b, err := WriteWord(d)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyOf(t, b))
	// Four rows of three cells each.
	if n := strings.Count(body, "<w:tc>"); n != 12 {
		t.Errorf("%d cells, want 12:\n%s", n, body)
	}
	// And no cell without a paragraph in it.
	if strings.Contains(body, "</w:tcPr></w:tc>") {
		t.Errorf("a cell has no paragraph:\n%s", body)
	}
	if _, err := ParseWord(b, opc.Limits{}); err != nil {
		t.Fatalf("it could not be read: %v", err)
	}
}

// A MathBlock's TeX and a RawBlock's text are kept, because richdoc's own
// PlainText contributes nothing for either and the choice is the source or
// silence. A ThematicBreak has no text to keep and becomes a gap.
func TestTheBlocksWhosePlainTextIsEmpty(t *testing.T) {
	b, err := WriteWord(&richdoc.Document{Blocks: []richdoc.Block{
		richdoc.MathBlock{TeX: `E = mc^2`},
		richdoc.RawBlock{Format: "html", Text: "<hr>"},
		richdoc.ThematicBreak{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	text := richdoc.PlainText(got)
	for _, want := range []string{"E = mc^2", "<hr>"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q was dropped:\n%s", want, text)
		}
	}
}

// ⛔ xml:space="preserve" on every run, asserted by its SPELLING. A reader
// that keeps the whitespace it was given — ours does — reads the same text
// either way, so the round trip cannot see this. A reader that follows the
// standard trims a run without it, and a document rebuilt from runs that each
// lost their edges reads as onewordafteranother.
//
// Third time this pattern has come up here: our own reader is more lenient
// than the standard, so the thing that has to be asserted is what was
// written. The control is a .docx Word itself produced, whose runs carry it.
func TestEveryRunSaysToKeepItsSpaces(t *testing.T) {
	b, err := WriteWord(sample())
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyOf(t, b))
	// ⛔ `<w:t` is not the count: it also matches <w:tbl>, <w:tc>, <w:tr> and
	// <w:tblGrid>, and counting those said 15 of 42 when every run of the 15
	// carried it. The question is whether any text element LACKS it.
	keep := strings.Count(body, `<w:t xml:space="preserve">`)
	if keep == 0 {
		t.Fatalf("no runs at all:\n%s", body)
	}
	if n := strings.Count(body, "<w:t>"); n != 0 {
		t.Errorf("%d runs do not say to keep their spaces:\n%s", n, body)
	}
	// And in the sheet, for the same reason.
	x, err := WriteWorkbook(sample())
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(x, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := p.Part("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(sheet), "<t>"); n != 0 {
		t.Errorf("%d sheet cells do not say to keep their spaces:\n%s", n, sheet)
	}
	if !strings.Contains(string(sheet), `<t xml:space="preserve">`) {
		t.Errorf("no cells at all in the sheet:\n%s", sheet)
	}
}
