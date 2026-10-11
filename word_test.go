// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"os"
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// ⛔ WRITTEN BY LIBREOFFICE, not composed here. Several of the rules in
// word.go exist only because this file disagreed with the obvious reading of
// the standard — most sharply the one about hyperlinks, which arrive as
// EXTERNAL relationships and would be lost by a reader that resolved them to a
// part.
const richPath = "testdata/rich.docx"

func parse(t *testing.T, path string) *richdoc.Document {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return d
}

// flatten renders a document to one string, marking the structures, so a test
// can assert what came out AND in what order.
func flatten(doc *richdoc.Document) string {
	var b strings.Builder
	writeBlocks(&b, doc.Blocks)
	return b.String()
}

func writeInlines(b *strings.Builder, is []richdoc.Inline) {
	for _, i := range is {
		switch v := i.(type) {
		case richdoc.Text:
			b.WriteString(v.Value)
		case richdoc.Strong:
			b.WriteString("**")
			writeInlines(b, v.Inlines)
			b.WriteString("**")
		case richdoc.Emph:
			b.WriteString("*")
			writeInlines(b, v.Inlines)
			b.WriteString("*")
		case richdoc.Strikethrough:
			b.WriteString("~~")
			writeInlines(b, v.Inlines)
			b.WriteString("~~")
		case richdoc.Link:
			b.WriteString("[")
			writeInlines(b, v.Inlines)
			b.WriteString("](" + v.URL + ")")
		case richdoc.Image:
			b.WriteString("[img " + v.URL + "]")
		case richdoc.LineBreak:
			b.WriteString("\\n")
		}
	}
}

func writeBlocks(b *strings.Builder, bs []richdoc.Block) {
	for _, bl := range bs {
		switch v := bl.(type) {
		case richdoc.Heading:
			b.WriteString("\n" + strings.Repeat("#", v.Level) + " ")
			writeInlines(b, v.Inlines)
			b.WriteString("\n")
		case richdoc.Paragraph:
			writeInlines(b, v.Inlines)
			b.WriteString("\n")
		case richdoc.List:
			marker := "- "
			if v.Ordered {
				marker = "1. "
			}
			for _, it := range v.Items {
				b.WriteString(marker)
				writeBlocks(b, it.Blocks)
			}
		case richdoc.Table:
			for _, r := range v.Rows {
				for _, c := range r {
					b.WriteString("|" + strings.TrimSpace(render(c.Content())))
				}
				b.WriteString("|\n")
			}
		case richdoc.RawBlock:
			b.WriteString("[raw " + v.Format + "]\n")
		}
	}
}

func render(bs []richdoc.Block) string {
	var b strings.Builder
	writeBlocks(&b, bs)
	return b.String()
}

func TestWordFromLibreOffice(t *testing.T) {
	got := flatten(parse(t, richPath))
	for _, want := range []string{
		"# Top heading",
		"## Second level",
		"- bullet one",
		"- bullet two",
		"|format|tools|",
		"|docx|1|",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
	if strings.Contains(got, "[raw") {
		t.Errorf("something came out raw:\n%s", got)
	}
}

// ⛔ A heading's level comes from the paragraph's STYLE NAME, which is the
// opposite of ODF's text:h with its outline-level attribute: there is no
// heading element in WordprocessingML, only a paragraph wearing a style. The
// two levels are asserted separately so that a reader which found "a heading"
// but always said level one would fail.
func TestHeadingLevelsComeFromTheStyleName(t *testing.T) {
	doc := parse(t, richPath)
	var levels []int
	for _, b := range doc.Blocks {
		if h, ok := b.(richdoc.Heading); ok {
			levels = append(levels, h.Level)
		}
	}
	if len(levels) != 2 || levels[0] != 1 || levels[1] != 2 {
		t.Errorf("heading levels = %v, want [1 2]", levels)
	}
}

func TestHeadingLevelFromAStyleId(t *testing.T) {
	for id, want := range map[string]int{
		"Heading1": 1, "Heading9": 9, "heading3": 3,
		"Heading 2": 2, "Heading-4": 4,
	} {
		got, ok := headingLevel(id)
		if !ok || got != want {
			t.Errorf("headingLevel(%q) = %d, %v; want %d", id, got, ok, want)
		}
	}
	for _, id := range []string{"Normal", "", "Heading0", "Heading10", "HeadingX", "Title"} {
		if got, ok := headingLevel(id); ok {
			t.Errorf("headingLevel(%q) = %d, true; want not a heading", id, got)
		}
	}
}

// ⛔ The spaces at the ends of a w:t are part of the sentence — that is what
// xml:space="preserve" is for — and a reader that trimmed them runs words
// together. "plain then " + "bold" is three words; trimmed it is two.
func TestTheSpacesAroundAFormattedRunSurvive(t *testing.T) {
	got := flatten(parse(t, richPath))
	if !strings.Contains(got, "plain then **bold** then *italic* end.") {
		t.Errorf("the formatted line came out as:\n%s", got)
	}
}

// ⛔ A hyperlink arrives as an EXTERNAL relationship, so its Target is the URL
// itself. opc.Resolve refuses an external target — correctly — and a reader
// that called it here would lose every link in the document.
func TestAHyperlinkKeepsItsURL(t *testing.T) {
	got := flatten(parse(t, richPath))
	if !strings.Contains(got, "[link to example](https://example.com/x)") {
		t.Errorf("the link came out as:\n%s", got)
	}
}

func TestTheOrderOfTheDocumentIsKept(t *testing.T) {
	got := flatten(parse(t, richPath))
	top := strings.Index(got, "# Top heading")
	bold := strings.Index(got, "**bold**")
	second := strings.Index(got, "## Second level")
	bullet := strings.Index(got, "- bullet one")
	table := strings.Index(got, "|format|tools|")
	if !(top < bold && bold < second && second < bullet && bullet < table) {
		t.Errorf("out of order (%d %d %d %d %d):\n%s", top, bold, second, bullet, table, got)
	}
}

// ⛔ The fallback for a list whose format the file does not give is UNORDERED.
// LibreOffice wrote numFmt="none" for this very list, so a reader that treated
// "not bullet" as "numbered" would invent an order the document never had.
func TestAListWithNoFormatIsNotNumbered(t *testing.T) {
	doc := parse(t, richPath)
	found := false
	for _, b := range doc.Blocks {
		if l, ok := b.(richdoc.List); ok {
			found = true
			if l.Ordered {
				t.Error("a list whose numFmt is \"none\" came out numbered")
			}
			if len(l.Items) != 2 {
				t.Errorf("the list has %d items, want 2", len(l.Items))
			}
		}
	}
	if !found {
		t.Error("no list in the document, so this proves nothing")
	}
}

func TestOrderedReadsAFormat(t *testing.T) {
	for fmtName, want := range map[string]bool{
		"": false, "none": false, "bullet": false, "Bullet": false,
		"decimal": true, "lowerRoman": true, "upperLetter": true,
	} {
		if got := ordered(fmtName); got != want {
			t.Errorf("ordered(%q) = %v, want %v", fmtName, got, want)
		}
	}
}

func firstImage(t *testing.T, d *richdoc.Document) richdoc.Image {
	t.Helper()
	var found *richdoc.Image
	var walk func([]richdoc.Block)
	walkIn := func(is []richdoc.Inline) {
		for _, i := range is {
			if m, ok := i.(richdoc.Image); ok && found == nil {
				c := m
				found = &c
			}
		}
	}
	walk = func(bs []richdoc.Block) {
		for _, b := range bs {
			switch v := b.(type) {
			case richdoc.Paragraph:
				walkIn(v.Inlines)
			case richdoc.Heading:
				walkIn(v.Inlines)
			case richdoc.List:
				for _, it := range v.Items {
					walk(it.Blocks)
				}
			}
		}
	}
	walk(d.Blocks)
	if found == nil {
		t.Fatal("no image in the document")
	}
	return *found
}
