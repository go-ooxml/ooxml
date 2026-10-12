// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// The content types and relationship types a .docx is made of, taken from a
// .docx Word itself produced rather than from memory.
const (
	ctWordMain = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	ctStyles   = "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"
	ctNumber   = "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"

	relStyles = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"

	// nsW is WordprocessingML's namespace. The prefix is `w` by convention
	// and by every producer, and the readers of the world key off it.
	nsW = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

	// wordHeadings is how many heading levels styles.xml defines. Word has
	// nine built in; richdoc and every format it writes to have six, and a
	// level past the last one is written AS the last rather than dropped.
	wordHeadings = 6

	// bulletNumID is the numbering a bulleted list refers to. There is one,
	// because this writes one kind of list.
	bulletNumID = 1
)

// WriteWord writes a document as a WordprocessingML package (.docx).
//
// ⛔ What comes back is a package, not a rendering. Word decides how wide a
// column is, where a page breaks and what a heading looks like; this says
// which paragraph is a heading and which cells are a row, and the styles part
// below gives those names something to mean.
//
// ⛔ It is NOT a round trip with [ParseWord]. A document read from Word and
// written back out loses everything richdoc has no word for — the fonts, the
// margins, the section properties, the revision history — because richdoc is
// a document model and not a .docx model. Said here because "write what you
// read" is the obvious thing to expect and it is not what this does.
func WriteWord(d *richdoc.Document) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("ooxml: no document to write")
	}
	body, lists := wordBody(d.Blocks)
	parts := []opc.NewPart{
		{Name: "word/document.xml", ContentType: ctWordMain, Data: body},
		{Name: "word/styles.xml", ContentType: ctStyles, Data: wordStyles()},
	}
	rels := map[string][]opc.Rel{
		"": {{ID: "rId1", Type: opc.RelOfficeDocument, Target: "word/document.xml"}},
		"word/document.xml": {
			{ID: "rId1", Type: relStyles, Target: "styles.xml"},
		},
	}
	// ⛔ numbering.xml only when there IS a list. A .docx that declares a
	// numbering part it never refers to is valid and pointless; one that
	// refers to a numbering part that is not there is neither.
	if lists {
		parts = append(parts, opc.NewPart{
			Name: "word/numbering.xml", ContentType: ctNumber, Data: wordNumbering(),
		})
		rels["word/document.xml"] = append(rels["word/document.xml"],
			opc.Rel{ID: "rId2", Type: relNumbering, Target: "numbering.xml"})
	}
	return opc.Build(parts, rels)
}

// wordBody writes word/document.xml, and says whether anything in it is a
// list — which is what decides whether a numbering part is needed.
func wordBody(blocks []richdoc.Block) ([]byte, bool) {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<w:document xmlns:w=%q><w:body>`, nsW)
	w := &wordWriter{b: &b}
	w.blocks(blocks, 0)
	// ⛔ A body with nothing in it is not a valid document: Word wants at
	// least one paragraph, and a reader that finds none has nothing to put a
	// cursor in.
	if w.wrote == 0 {
		b.WriteString(`<w:p/>`)
	}
	b.WriteString(`</w:body></w:document>`)
	return b.Bytes(), w.lists
}

// a wordWriter accumulates the body, counting what it put out.
type wordWriter struct {
	b     *bytes.Buffer
	wrote int
	lists bool
}

// blocks writes a sequence of blocks. depth is how deep inside list items we
// are, which is what a nested list's indent is counted in.
func (w *wordWriter) blocks(blocks []richdoc.Block, depth int) {
	for _, blk := range blocks {
		switch n := blk.(type) {
		case richdoc.Heading:
			w.para(n.Inlines, headingStyle(n.Level), -1)
		case richdoc.Paragraph:
			w.para(n.Inlines, "", -1)
		case richdoc.List:
			w.lists = true
			for _, item := range n.Items {
				w.item(item, depth)
			}
		case richdoc.Table:
			w.table(n)
		case richdoc.BlockQuote:
			// A quotation is its blocks, one indent further in. Word has a
			// Quote style; using the indent instead keeps this to the styles
			// the file defines.
			w.blocks(n.Blocks, depth+1)
		case richdoc.CodeBlock:
			// Preformatted text, line by line: a paragraph per line, because
			// a paragraph is the only thing in WordprocessingML that holds a
			// line and `w:br` inside one would make it a single line that
			// wraps.
			for _, line := range splitLines(n.Text) {
				w.para([]richdoc.Inline{richdoc.Text{Value: line}}, "", -1)
			}
		case richdoc.MathBlock:
			// ⛔ The TeX, as text. Word has maths of its own (OMML) and this
			// does not write it, so the choice is the source or nothing —
			// and [richdoc.PlainText] contributes nothing for a MathBlock,
			// which is why this cannot go through the default below.
			w.para([]richdoc.Inline{richdoc.Txt(n.TeX)}, "", -1)
		case richdoc.RawBlock:
			// Likewise: raw content for some other format, kept as its text
			// rather than dropped.
			w.para([]richdoc.Inline{richdoc.Txt(n.Text)}, "", -1)
		default:
			// A ThematicBreak, and anything richdoc adds later. An empty
			// paragraph is a poor rendering of a horizontal rule and a
			// truthful one of a block this does not know — and it is a
			// paragraph, so a block type added upstream shows up as a gap
			// rather than disappearing without trace.
			w.para(nil, "", -1)
		}
	}
}

// item writes one list item: its first paragraph carries the bullet, and
// anything after it is written plainly so that a wrapped item does not become
// two bullets.
func (w *wordWriter) item(item richdoc.ListItem, depth int) {
	for i, blk := range item.Blocks {
		p, ok := blk.(richdoc.Paragraph)
		if i == 0 && ok {
			w.para(p.Inlines, "ListParagraph", depth)
			continue
		}
		w.blocks([]richdoc.Block{blk}, depth+1)
	}
	if len(item.Blocks) == 0 {
		// An item with nothing in it is still an item, and a bullet with no
		// text is what the author wrote.
		w.para(nil, "ListParagraph", depth)
	}
}

// para writes one paragraph. style names a w:pStyle, and bullet is the list
// level this paragraph is a bullet at, or -1 for a paragraph that is not one.
func (w *wordWriter) para(inlines []richdoc.Inline, style string, bullet int) {
	w.wrote++
	w.b.WriteString(`<w:p>`)
	if style != "" || bullet >= 0 {
		w.b.WriteString(`<w:pPr>`)
		if style != "" {
			fmt.Fprintf(w.b, `<w:pStyle w:val=%q/>`, style)
		}
		if bullet >= 0 {
			fmt.Fprintf(w.b,
				`<w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`,
				bullet, bulletNumID)
		}
		w.b.WriteString(`</w:pPr>`)
	}
	w.runs(inlines)
	w.b.WriteString(`</w:p>`)
}

// runs writes a paragraph's contents.
func (w *wordWriter) runs(inlines []richdoc.Inline) {
	for _, in := range inlines {
		switch n := in.(type) {
		case richdoc.Strong:
			w.run(inlineText(n.Inlines), "b")
		case richdoc.Emph:
			w.run(inlineText(n.Inlines), "i")
		case richdoc.Code:
			w.run(n.Value, "")
		default:
			w.run(textOfInline(in), "")
		}
	}
}

// run writes one run. mark is "b", "i" or empty.
func (w *wordWriter) run(text, mark string) {
	if text == "" {
		return
	}
	w.b.WriteString(`<w:r>`)
	if mark != "" {
		fmt.Fprintf(w.b, `<w:rPr><w:%s/></w:rPr>`, mark)
	}
	// ⛔ xml:space="preserve", always. Without it a run whose text begins or
	// ends with a space loses it, and a document rebuilt from runs that each
	// dropped their edges reads as onewordafteranother.
	w.b.WriteString(`<w:t xml:space="preserve">`)
	esc(w.b, text)
	w.b.WriteString(`</w:t></w:r>`)
}

// table writes one grid.
func (w *wordWriter) table(t richdoc.Table) {
	rows := append([][]richdoc.Cell{}, t.Rows...)
	if len(t.Header) > 0 {
		rows = append([][]richdoc.Cell{t.Header}, rows...)
	}
	if len(rows) == 0 {
		return
	}
	w.wrote++
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	// The borders are the ones a .docx Word produced carries, and a table
	// with none is a table a reader cannot see the shape of.
	w.b.WriteString(`<w:tbl><w:tblPr><w:tblW w:type="auto" w:w="0"/><w:tblBorders>`)
	for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		fmt.Fprintf(w.b, `<w:%s w:val="single" w:color="auto" w:sz="4"/>`, side)
	}
	w.b.WriteString(`</w:tblBorders></w:tblPr><w:tblGrid>`)
	for i := 0; i < cols; i++ {
		// A width of nothing with w:tblW auto lets Word lay the columns out,
		// which is Word's business and not ours.
		w.b.WriteString(`<w:gridCol/>`)
	}
	w.b.WriteString(`</w:tblGrid>`)
	for _, row := range rows {
		w.b.WriteString(`<w:tr>`)
		for i := 0; i < cols; i++ {
			w.b.WriteString(`<w:tc><w:tcPr><w:tcW w:type="auto" w:w="0"/></w:tcPr>`)
			if i < len(row) {
				w.cell(row[i])
			} else {
				// ⛔ Every row needs the same number of cells, and a cell
				// needs a paragraph: a w:tc with nothing in it is what makes
				// Word call a document corrupt.
				w.b.WriteString(`<w:p/>`)
			}
			w.b.WriteString(`</w:tc>`)
		}
		w.b.WriteString(`</w:tr>`)
	}
	w.b.WriteString(`</w:tbl>`)
	// A table has to be followed by a paragraph, or a table at the end of a
	// body leaves a reader with nowhere to put the cursor after it.
	w.b.WriteString(`<w:p/>`)
}

// cell writes one cell's contents, which richdoc.Cell says to read through
// Content so that a cell holding blocks is not read as an empty one.
func (w *wordWriter) cell(c richdoc.Cell) {
	blocks := c.Content()
	if len(blocks) == 0 {
		w.b.WriteString(`<w:p/>`)
		return
	}
	before := w.wrote
	w.blocks(blocks, 0)
	if w.wrote == before {
		// The blocks said nothing a paragraph could hold. The cell still
		// needs one.
		w.b.WriteString(`<w:p/>`)
	}
}

// headingStyle is the style name for a heading level, clamped to the levels
// styles.xml defines. A level past the last is written AS the last, because
// text somebody made a heading is a heading whatever depth they gave it.
func headingStyle(level int) string {
	if level < 1 {
		level = 1
	}
	if level > wordHeadings {
		level = wordHeadings
	}
	return "Heading" + strconv.Itoa(level)
}

// wordStyles writes word/styles.xml.
//
// ⛔ The heading styles are DEFINED here rather than left to Word's latent
// ones. A w:pStyle naming a style the package does not define works in Word,
// which knows "Heading1" already — and shows as plain text in a reader that
// does not, which is most of them. Defining them costs a few hundred bytes
// and is the difference between a heading and a paragraph everywhere but Word.
func wordStyles() []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<w:styles xmlns:w=%q>`, nsW)
	for i := 1; i <= wordHeadings; i++ {
		// The sizes are half-points, as WordprocessingML counts them: 32 is
		// 16pt for a first-level heading down to 20 (10pt) for a sixth.
		size := 34 - 2*i
		fmt.Fprintf(&b, `<w:style w:type="paragraph" w:styleId="Heading%d">`+
			`<w:name w:val="heading %d"/><w:basedOn w:val="Normal"/>`+
			`<w:pPr><w:outlineLvl w:val="%d"/><w:keepNext/>`+
			`<w:spacing w:before="240" w:after="120"/></w:pPr>`+
			`<w:rPr><w:b/><w:sz w:val="%d"/></w:rPr></w:style>`,
			i, i, i-1, size)
	}
	b.WriteString(`<w:style w:type="paragraph" w:styleId="Normal">` +
		`<w:name w:val="Normal"/></w:style>`)
	b.WriteString(`<w:style w:type="paragraph" w:styleId="ListParagraph">` +
		`<w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/>` +
		`<w:pPr><w:ind w:left="720"/></w:pPr></w:style>`)
	b.WriteString(`</w:styles>`)
	return b.Bytes()
}

// wordNumbering writes word/numbering.xml: one bulleted numbering, with a
// level for each depth a list can nest to.
//
// A numbering is two things — an abstract definition and a w:num that points
// at it — and a w:numId naming a w:num that is not there is a list Word shows
// without its bullets.
func wordNumbering() []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<w:numbering xmlns:w=%q><w:abstractNum w:abstractNumId="0">`, nsW)
	for lvl := 0; lvl < 9; lvl++ {
		fmt.Fprintf(&b, `<w:lvl w:ilvl="%d"><w:numFmt w:val="bullet"/>`+
			`<w:lvlText w:val="%s"/><w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr>`+
			`<w:rPr><w:rFonts w:ascii="Symbol" w:hAnsi="Symbol"/></w:rPr></w:lvl>`,
			lvl, bulletFor(lvl), 720*(lvl+1))
	}
	b.WriteString(`</w:abstractNum>`)
	fmt.Fprintf(&b, `<w:num w:numId="%d"><w:abstractNumId w:val="0"/></w:num>`, bulletNumID)
	b.WriteString(`</w:numbering>`)
	return b.Bytes()
}

// bulletFor is the glyph a nesting level is marked with, in the Symbol font
// Word uses for them: a filled disc, a hollow circle, then a square.
func bulletFor(level int) string {
	switch level % 3 {
	case 0:
		return ""
	case 1:
		return "o"
	default:
		return ""
	}
}

// esc appends text escaped for XML.
//
// [xml.EscapeText] returns whatever error the writer it was given returned,
// and a [bytes.Buffer] never returns one — so the error is dropped here, in
// one place with the reason beside it, rather than checked at every call.
func esc(b *bytes.Buffer, s string) {
	_ = xml.EscapeText(b, []byte(s))
}

// textOf and inlineText read a node's words back.
//
// ⛔ Both go through [richdoc.PlainText] rather than walking the tree here,
// because what counts as a node's text is richdoc's rule and there must be
// one of it: a footnote contributes its body, an anchor contributes its
// visible inlines and not its identifier, an image contributes nothing. A
// second opinion written here would differ from the first the day richdoc
// gains a node.
func textOf(b richdoc.Block) string {
	return richdoc.PlainText(&richdoc.Document{Blocks: []richdoc.Block{b}})
}

func inlineText(in []richdoc.Inline) string {
	if len(in) == 0 {
		return ""
	}
	return textOf(richdoc.Paragraph{Inlines: in})
}

func textOfInline(in richdoc.Inline) string {
	return inlineText([]richdoc.Inline{in})
}

// splitLines is a block of preformatted text as its lines, with the trailing
// newline that a code block conventionally ends with left out: it is a
// terminator and not an empty last line.
func splitLines(s string) []string {
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
