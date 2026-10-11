// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// docxOf wraps a w:body in a package.
//
// ⛔ These fixtures are composed here, and the distinction matters: the RULES
// in word.go were all read off files LibreOffice wrote, and these test the
// reader's handling of things the standard puts in a file that a converter
// will not produce on demand — a field code, a tracked deletion, a toggle
// property switched off. Discovering a rule needs a real file; exercising a
// branch does not.
func docxOf(t *testing.T, body string, extra map[string]string) []byte {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="` + opc.RelOfficeDocument + `" Target="word/document.xml"/></Relationships>`,
		"word/document.xml": `<?xml version="1.0"?><w:document ` +
			`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<w:body>` + body + `</w:body></w:document>`,
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

func parseBody(t *testing.T, body string, extra map[string]string) string {
	t.Helper()
	d, err := ParseWord(docxOf(t, body, extra), opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return flatten(d)
}

// ⛔ A field's INSTRUCTIONS are not text. "PAGE \* MERGEFORMAT" is what the
// file says; "7" is what a reader shows. Emitting the instructions puts
// typesetting directives into the prose.
func TestAFieldsInstructionsAreNotText(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:t>page </w:t></w:r>`+
		`<w:r><w:instrText xml:space="preserve"> PAGE \* MERGEFORMAT </w:instrText></w:r>`+
		`<w:r><w:t>7</w:t></w:r></w:p>`, nil)
	if strings.Contains(got, "MERGEFORMAT") || strings.Contains(got, "PAGE") {
		t.Errorf("a field's instructions reached the document: %q", got)
	}
	if !strings.Contains(got, "page 7") {
		t.Errorf("the text around the field was lost: %q", got)
	}
}

// ⛔ Text somebody DELETED with track changes on is in the file so the change
// can be rejected. Emitting it puts removed sentences back into the document —
// which is a confidentiality problem and not only a correctness one.
func TestDeletedTextStaysDeletedAndInsertedTextArrives(t *testing.T) {
	got := parseBody(t, `<w:p>`+
		`<w:r><w:t xml:space="preserve">the fee is </w:t></w:r>`+
		`<w:del><w:r><w:delText>one million</w:delText></w:r></w:del>`+
		`<w:ins><w:r><w:t>ten</w:t></w:r></w:ins>`+
		`<w:r><w:t xml:space="preserve"> pounds</w:t></w:r></w:p>`, nil)
	if strings.Contains(got, "one million") {
		t.Errorf("deleted text reached the document: %q", got)
	}
	if !strings.Contains(got, "the fee is ten pounds") {
		t.Errorf("want \"the fee is ten pounds\", got %q", got)
	}
}

// ⛔ w:val="0" TURNS A TOGGLE OFF. A reader that read the element's presence
// as "on" would make bold every run whose style switched bold off.
func TestAToggleSwitchedOffIsOff(t *testing.T) {
	got := parseBody(t, `<w:p>`+
		`<w:r><w:rPr><w:b/></w:rPr><w:t>on</w:t></w:r>`+
		`<w:r><w:rPr><w:b w:val="0"/></w:rPr><w:t>off</w:t></w:r>`+
		`<w:r><w:rPr><w:b w:val="false"/></w:rPr><w:t>also-off</w:t></w:r>`+
		`</w:p>`, nil)
	if !strings.Contains(got, "**on**") {
		t.Errorf("a plain w:b did not make text bold: %q", got)
	}
	if strings.Contains(got, "**off**") || strings.Contains(got, "**also-off**") {
		t.Errorf("w:val=\"0\" did not switch bold off: %q", got)
	}
}

// ⛔ Underline is NOT emphasis. In a legal document it is a defined term; in a
// web page it is a link that is not one. Borrowing Emph or Strong for it would
// make a document say something it does not.
func TestUnderlineIsNotBorrowedFromEmphasis(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:rPr><w:u w:val="single"/></w:rPr>`+
		`<w:t>defined term</w:t></w:r></w:p>`, nil)
	if got != "defined term\n" {
		t.Errorf("underline became %q, want plain text", got)
	}
}

func TestStrikethroughSurvives(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:rPr><w:strike/></w:rPr><w:t>gone</w:t></w:r></w:p>`, nil)
	if !strings.Contains(got, "~~gone~~") {
		t.Errorf("got %q", got)
	}
}

// ⛔ An empty paragraph is dropped. Word is full of them — they are how a
// person makes vertical space — and a Paragraph with no inlines reaches a PDF
// as a line of nothing, which is not what the page showed.
func TestEmptyParagraphsAreDropped(t *testing.T) {
	d, err := ParseWord(docxOf(t, `<w:p/><w:p><w:r><w:t>one</w:t></w:r></w:p><w:p/><w:p/>`, nil), opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 1 {
		t.Errorf("%d blocks, want 1: %v", len(d.Blocks), d.Blocks)
	}
}

// ⛔ Two lists back to back with DIFFERENT numbering are two lists. Joining
// them would renumber the second from where the first left off.
func TestTwoListsWithDifferentNumberingStayTwo(t *testing.T) {
	item := func(numID, text string) string {
		return `<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="` + numID +
			`"/></w:numPr></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	d, err := ParseWord(docxOf(t,
		item("1", "a")+item("1", "b")+item("2", "c"), nil), opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var lists []richdoc.List
	for _, b := range d.Blocks {
		if l, ok := b.(richdoc.List); ok {
			lists = append(lists, l)
		}
	}
	if len(lists) != 2 {
		t.Fatalf("%d lists, want 2", len(lists))
	}
	if len(lists[0].Items) != 2 || len(lists[1].Items) != 1 {
		t.Errorf("items %d and %d, want 2 and 1", len(lists[0].Items), len(lists[1].Items))
	}
}

// The numbering chain, which is two hops: numPr/numId → w:num → w:abstractNum
// → its level-zero w:numFmt.
func TestANumberedListIsNumberedWhenTheFileSaysSo(t *testing.T) {
	numbering := `<?xml version="1.0"?><w:numbering ` +
		`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:abstractNum w:abstractNumId="7"><w:lvl w:ilvl="0">` +
		`<w:numFmt w:val="decimal"/></w:lvl></w:abstractNum>` +
		`<w:num w:numId="3"><w:abstractNumId w:val="7"/></w:num></w:numbering>`
	body := `<w:p><w:pPr><w:numPr><w:numId w:val="3"/></w:numPr></w:pPr>` +
		`<w:r><w:t>first</w:t></w:r></w:p>`
	got := parseBody(t, body, map[string]string{"word/numbering.xml": numbering})
	if !strings.Contains(got, "1. first") {
		t.Errorf("a decimal list came out as %q", got)
	}
	// And the control: the same body with no numbering part is a bullet list,
	// so the test above is the numbering and not the paragraph.
	if got := parseBody(t, body, nil); !strings.Contains(got, "- first") {
		t.Errorf("with no numbering part the list came out as %q", got)
	}
}

// ⛔ The first row is NOT assumed to be a header. Taking it regardless would
// turn a two-row table of data into one row of data under a heading that is
// data.
func TestTheFirstRowIsNotAssumedToBeAHeader(t *testing.T) {
	d, err := ParseWord(docxOf(t,
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc></w:tr>`+
			`<w:tr><w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`, nil), opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range d.Blocks {
		if tb, ok := b.(richdoc.Table); ok {
			if len(tb.Header) != 0 {
				t.Errorf("a header was invented: %v", tb.Header)
			}
			if len(tb.Rows) != 2 {
				t.Errorf("%d rows, want 2", len(tb.Rows))
			}
		}
	}
}

func TestALineBreakAndATabSurvive(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:t>a</w:t><w:br/><w:t>b</w:t><w:tab/><w:t>c</w:t></w:r></w:p>`, nil)
	if !strings.Contains(got, `a\nb`) || !strings.Contains(got, "b\tc") {
		t.Errorf("got %q", got)
	}
}

// A hyperlink that points nowhere is its text: a Link with an empty URL would
// make a PDF with a dead clickable region on it.
func TestAHyperlinkWithNoTargetIsItsText(t *testing.T) {
	got := parseBody(t, `<w:p><w:hyperlink r:id="rIdNope">`+
		`<w:r><w:t>words</w:t></w:r></w:hyperlink></w:p>`, nil)
	if strings.Contains(got, "](") {
		t.Errorf("a link with no target became a link: %q", got)
	}
	if !strings.Contains(got, "words") {
		t.Errorf("its text was lost: %q", got)
	}
}

func TestAnAnchorBecomesAFragment(t *testing.T) {
	got := parseBody(t, `<w:p><w:hyperlink w:anchor="top">`+
		`<w:r><w:t>back up</w:t></w:r></w:hyperlink></w:p>`, nil)
	if !strings.Contains(got, "[back up](#top)") {
		t.Errorf("got %q", got)
	}
}

// outlineLvl is ZERO-based here and one-based in richdoc: a reader that passed
// it through would make every top heading a level zero.
func TestOutlineLevelIsOneBasedInRichdoc(t *testing.T) {
	got := parseBody(t, `<w:p><w:pPr><w:outlineLvl w:val="0"/></w:pPr>`+
		`<w:r><w:t>top</w:t></w:r></w:p>`, nil)
	if !strings.Contains(got, "# top") || strings.Contains(got, "## top") {
		t.Errorf("outlineLvl 0 came out as %q, want a level-one heading", got)
	}
}

func TestRefusals(t *testing.T) {
	if _, err := ParseWord([]byte("not a zip"), opc.Limits{}); err == nil {
		t.Error("rubbish was parsed")
	}
	// A package with no officeDocument relationship.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte("<x/>"))
	zw.Close()
	if _, err := ParseWord(buf.Bytes(), opc.Limits{}); err == nil {
		t.Error("a package naming no main part was parsed")
	}
	// A main part that is not a WordprocessingML document.
	if _, err := ParseWord(docxOf(t, "", map[string]string{
		"word/document.xml": `<?xml version="1.0"?><nothing/>`,
	}), opc.Limits{}); err == nil {
		t.Error("a document with no w:body was parsed")
	}
	// The ceiling reaches here from opc.
	if _, err := ParseWord(docxOf(t, `<w:p/>`, nil), opc.Limits{MaxPartBytes: 8}); err == nil {
		t.Error("a part past the ceiling was parsed")
	}
}

// ⛔ The depth guard is a token pass of its own, because the stack a deep
// document overflows belongs to encoding/xml and not to this package: a guard
// where our own code recurses never runs.
func TestADocumentNestedTooDeepIsRefused(t *testing.T) {
	deep := strings.Repeat("<w:tbl><w:tr><w:tc>", maxDepth) +
		strings.Repeat("</w:tc></w:tr></w:tbl>", maxDepth)
	_, err := ParseWord(docxOf(t, deep, nil), opc.Limits{})
	if err == nil {
		t.Fatal("a document nested past the ceiling was parsed")
	}
	if !strings.Contains(err.Error(), "nest") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	// And the control: just under the ceiling is read, so the refusal above is
	// the depth and not the size.
	ok := strings.Repeat("<w:tbl><w:tr><w:tc>", 20) + `<w:p><w:r><w:t>deep</w:t></w:r></w:p>` +
		strings.Repeat("</w:tc></w:tr></w:tbl>", 20)
	if _, err := ParseWord(docxOf(t, ok, nil), opc.Limits{}); err != nil {
		t.Errorf("a document twenty deep was refused: %v", err)
	}
}
