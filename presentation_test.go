// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// ⛔ WRITTEN BY LIBREOFFICE. The rule this file settled is the one that makes
// a shared implementation wrong: a:rPr carries its formatting as ATTRIBUTES,
// so Word's runProps finds nothing here.
const pptxPath = "testdata/deck.pptx"

func parseDeck(t *testing.T, path string) *richdoc.Document {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParsePresentation(b, opc.Limits{})
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return d
}

func TestPresentationFromLibreOffice(t *testing.T) {
	got := flatten(parseDeck(t, pptxPath))
	for _, want := range []string{
		"# Slide 1",
		"What a reader needs from a deck",
		"the words, in order",
		"which slide they were on",
		"# Slide 2",
		"A table on a slide",
		"|format|tools|",
		"|odp|1|",
		"text in a shape",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestTheSlidesComeOutInOrder(t *testing.T) {
	got := flatten(parseDeck(t, pptxPath))
	one := strings.Index(got, "# Slide 1")
	words := strings.Index(got, "the words, in order")
	two := strings.Index(got, "# Slide 2")
	table := strings.Index(got, "|format|tools|")
	if !(one < words && words < two && two < table) {
		t.Errorf("out of order (%d %d %d %d):\n%s", one, words, two, table, got)
	}
}

// ⛔ The order comes from p:sldIdLst, NOT from the filenames. This builds a
// deck whose list is the reverse of its filenames, which is what a deck looks
// like after somebody has moved a slide: the names stay where they were.
//
// Without this test, a reader sorting by filename would pass every assertion
// above — the converted fixture happens to have both orders agree.
func TestTheOrderIsTheListsAndNotTheFilenames(t *testing.T) {
	pkg := pptxOf(t, map[string]string{
		"ppt/slides/slide1.xml": slideWith("first by name"),
		"ppt/slides/slide2.xml": slideWith("second by name"),
	}, []string{"rId2", "rId1"}, map[string]string{
		"rId1": "slides/slide1.xml",
		"rId2": "slides/slide2.xml",
	})
	d, err := ParsePresentation(pkg, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got := flatten(d)
	byName := strings.Index(got, "first by name")
	byList := strings.Index(got, "second by name")
	if byList > byName {
		t.Errorf("the slides came out in filename order, not list order:\n%s", got)
	}
	// And the slide numbering follows the LIST too: the one the list puts
	// first is Slide 1, whatever it is called.
	if !strings.Contains(got, "# Slide 1\nsecond by name") {
		t.Errorf("the numbering does not follow the list:\n%s", got)
	}
}

// ⛔⛔ The trap that makes a shared implementation wrong: a:rPr carries b, i, u
// and strike as ATTRIBUTES, where w:rPr carries them as child elements. A
// reader that reused runProps here would find no formatting at all.
func TestDrawingMlFormattingIsAttributes(t *testing.T) {
	for rPr, want := range map[string]string{
		`b="1"`:              "**x**",
		`i="1"`:              "*x*",
		`strike="sngStrike"`: "~~x~~",
		`b="1" i="1"`:        "***x***",
	} {
		pkg := pptxOf(t, map[string]string{
			"ppt/slides/slide1.xml": `<?xml version="1.0"?><p:sld ` +
				`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
				`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
				`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r>` +
				`<a:rPr ` + rPr + `/><a:t>x</a:t></a:r></a:p></p:txBody></p:sp>` +
				`</p:spTree></p:cSld></p:sld>`,
		}, []string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
		d, err := ParsePresentation(pkg, opc.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if got := flatten(d); !strings.Contains(got, want) {
			t.Errorf("a:rPr %s came out as %q, want %q", rPr, got, want)
		}
	}
	// And the off values, which are NAMED and not booleans: a reader treating
	// the attribute's presence as "on" would make every run of a converted
	// deck bold, struck through and underlined at once — LibreOffice writes
	// b="0" strike="noStrike" u="none" on all of them.
	pkg := pptxOf(t, map[string]string{
		"ppt/slides/slide1.xml": `<?xml version="1.0"?><p:sld ` +
			`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
			`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
			`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r>` +
			`<a:rPr b="0" i="0" strike="noStrike" u="none"/><a:t>plain</a:t>` +
			`</a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
	}, []string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
	d, err := ParsePresentation(pkg, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := flatten(d); !strings.Contains(got, "\nplain\n") {
		t.Errorf("the off values were read as on: %q", got)
	}
}

func TestDrawingRunStyleReadsTheNamedValues(t *testing.T) {
	for _, c := range []struct {
		attrs  string
		expect runStyle
	}{
		{`b="1"`, runStyle{bold: true}},
		{`b="true"`, runStyle{bold: true}},
		{`b="0"`, runStyle{}},
		{`u="sng"`, runStyle{underline: true}},
		{`u="none"`, runStyle{}},
		{`u=""`, runStyle{}},
		{`strike="dblStrike"`, runStyle{strike: true}},
		{`strike="noStrike"`, runStyle{}},
		{``, runStyle{}},
	} {
		se := startOf(t, `<a:rPr xmlns:a="y" `+c.attrs+`/>`)
		if got := drawingRunStyle(se); got != c.expect {
			t.Errorf("a:rPr %s = %+v, want %+v", c.attrs, got, c.expect)
		}
	}
}

// ⛔ a:buNone says a paragraph is NOT a list item, and it must be honoured: a
// converted deck puts marL and indent on every paragraph of a text box, so an
// indent alone is not a bullet.
func TestBulletsAreWhatTheFileSaysTheyAre(t *testing.T) {
	para := func(pPr, text string) string {
		return `<a:p>` + pPr + `<a:r><a:t>` + text + `</a:t></a:r></a:p>`
	}
	body := `<?xml version="1.0"?><p:sld ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody>` +
		para(`<a:pPr marL="216000" indent="-216000"><a:buChar char="-"/></a:pPr>`, "a bullet") +
		para(`<a:pPr marL="216000" indent="-216000"><a:buAutoNum type="arabicPeriod"/></a:pPr>`, "a number") +
		para(`<a:pPr marL="216000" indent="-216000"><a:buNone/></a:pPr>`, "not a bullet") +
		`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	pkg := pptxOf(t, map[string]string{"ppt/slides/slide1.xml": body},
		[]string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
	d, err := ParsePresentation(pkg, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got := flatten(d)
	if !strings.Contains(got, "- a bullet") {
		t.Errorf("buChar did not make a bullet: %q", got)
	}
	if !strings.Contains(got, "1. a number") {
		t.Errorf("buAutoNum did not make a numbered item: %q", got)
	}
	if strings.Contains(got, "- not a bullet") || strings.Contains(got, "1. not a bullet") {
		t.Errorf("buNone was ignored: %q", got)
	}
	// ⛔ And the two lists are TWO: a bulleted run and a numbered run back to
	// back are not one list, and joining them would renumber the second.
	var lists int
	for _, b := range d.Blocks {
		if _, ok := b.(richdoc.List); ok {
			lists++
		}
	}
	if lists != 2 {
		t.Errorf("%d lists, want 2", lists)
	}
}

func TestPresentationRefusals(t *testing.T) {
	if _, err := ParsePresentation([]byte("not a zip"), opc.Limits{}); err == nil {
		t.Error("rubbish was parsed")
	}
	// A presentation whose list is empty names no slides.
	pkg := pptxOf(t, nil, nil, nil)
	if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
		t.Error("a presentation with no slides was parsed")
	}
	// A sldId pointing at a relationship that is not a slide is skipped, and
	// with nothing left the presentation names no slides.
	pkg = pptxOf(t, nil, []string{"rIdNope"}, nil)
	if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
		t.Error("a list of stale entries was parsed")
	}
	// The ceiling reaches here.
	pkg = pptxOf(t, map[string]string{"ppt/slides/slide1.xml": slideWith("x")},
		[]string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
	if _, err := ParsePresentation(pkg, opc.Limits{MaxPartBytes: 8}); err == nil {
		t.Error("a part past the ceiling was parsed")
	}
}

// slideWith is a one-paragraph slide.
func slideWith(text string) string {
	return `<?xml version="1.0"?><p:sld ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + text +
		`</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
}

// pptxOf builds a presentation whose slide LIST order is given separately from
// the slide parts, so a test can make the two disagree.
func pptxOf(t *testing.T, slides map[string]string, order []string, targets map[string]string) []byte {
	t.Helper()
	var list, rels strings.Builder
	for i, id := range order {
		list.WriteString(`<p:sldId id="` + strconv.Itoa(256+i) + `" r:id="` + id + `"/>`)
	}
	for id, target := range targets {
		rels.WriteString(`<Relationship Id="` + id + `" Type="` + relSlide +
			`" Target="` + target + `"/>`)
	}
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rIdMain" Type="` + opc.RelOfficeDocument +
			`" Target="ppt/presentation.xml"/></Relationships>`,
		"ppt/presentation.xml": `<?xml version="1.0"?><p:presentation ` +
			`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<p:sldIdLst>` + list.String() + `</p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<?xml version="1.0"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			rels.String() + `</Relationships>`,
	}
	for name, body := range slides {
		parts[name] = body
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

// startOf parses one element and hands back its start token, so a test can
// exercise an attribute reader directly.
func startOf(t *testing.T, s string) xml.StartElement {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se
		}
	}
}

// A line break and a field on a slide. ⛔ A slide's a:fld holds in its a:t the
// value a viewer SHOWED — unlike Word's w:instrText, which holds the
// instructions — so it is read rather than dropped. The two formats put
// opposite things in the same place.
func TestABreakAndAFieldOnASlide(t *testing.T) {
	body := `<?xml version="1.0"?><p:sld ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody><a:p>` +
		`<a:r><a:t>line</a:t></a:r><a:br/><a:r><a:t>two</a:t></a:r>` +
		`<a:fld id="x" type="slidenum"><a:t>7</a:t></a:fld>` +
		`</a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	pkg := pptxOf(t, map[string]string{"ppt/slides/slide1.xml": body},
		[]string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
	d, err := ParsePresentation(pkg, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got := flatten(d)
	if !strings.Contains(got, `line\ntwo`) {
		t.Errorf("the break was lost: %q", got)
	}
	if !strings.Contains(got, "7") {
		t.Errorf("the field's value was dropped: %q", got)
	}
}

// A slide that holds nothing but an empty paragraph contributes its heading
// and no body, rather than a line of nothing.
func TestAnEmptySlideParagraphIsDropped(t *testing.T) {
	body := `<?xml version="1.0"?><p:sld ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody><a:p/><a:p><a:r><a:t></a:t></a:r></a:p>` +
		`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	pkg := pptxOf(t, map[string]string{"ppt/slides/slide1.xml": body},
		[]string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
	d, err := ParsePresentation(pkg, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 1 {
		t.Errorf("%d blocks, want 1 (the heading): %v", len(d.Blocks), d.Blocks)
	}
}

// ⛔ Every walker on a slide has its own error return for a token that does
// not arrive, and a reader that returned nil there would report a truncated
// deck as a complete one.
func TestTruncationOnASlideIsAnError(t *testing.T) {
	head := `<?xml version="1.0"?><p:sld ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody>`
	for name, body := range map[string]string{
		"a paragraph":          `<a:p><a:r`,
		"a run":                `<a:p><a:r><a:t>x`,
		"run properties":       `<a:p><a:r><a:rPr b="1"><a:inner`,
		"something in a run":   `<a:p><a:r><a:unknown><a:inner`,
		"a break":              `<a:p><a:br><a:inner`,
		"paragraph properties": `<a:p><a:pPr><a:buChar char="-"><a:inner`,
		"something in a pPr":   `<a:p><a:pPr><a:unknown><a:inner`,
		"something in a p":     `<a:p><a:unknown><a:inner`,
		"a table":              `<a:tbl><a:tr`,
		"a row":                `<a:tbl><a:tr><a:tc`,
		"a cell":               `<a:tbl><a:tr><a:tc><a:txBody><a:p><a:r><a:t>x`,
		"something in a table": `<a:tbl><a:unknown><a:inner`,
		"something in a row":   `<a:tbl><a:tr><a:unknown><a:inner`,
		"the shape tree":       `<a:p><a:r><a:t>x</a:t></a:r></a:p>`,
	} {
		t.Run(name, func(t *testing.T) {
			pkg := pptxOf(t, map[string]string{"ppt/slides/slide1.xml": head + body},
				[]string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
			if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
				t.Error("a truncated slide parsed without an error")
			}
		})
	}
}

// The presentation part itself can be truncated, and the list can be
// malformed.
func TestABrokenPresentationPartIsAnError(t *testing.T) {
	for name, presentation := range map[string]string{
		"truncated": `<?xml version="1.0"?><p:presentation xmlns:p="x"><p:sldIdLst><p:sldId`,
		"not xml":   `<?xml version="1.0"?><p:presentation xmlns:p="x"><<<`,
	} {
		t.Run(name, func(t *testing.T) {
			pkg := pptxOf(t, nil, []string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
			// Replace the presentation part with the broken one.
			pkg = replacePart(t, pkg, "ppt/presentation.xml", presentation)
			if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
				t.Error("a broken presentation part parsed without an error")
			}
		})
	}
}

// ⛔ A presentation CLAIMING more slides than the ceiling is refused from the
// list, before any slide is opened: the list is a count the file chose.
func TestTooManySlidesIsRefused(t *testing.T) {
	var list strings.Builder
	for i := range maxSlides + 1 {
		list.WriteString(`<p:sldId id="` + strconv.Itoa(256+i) + `" r:id="rId1"/>`)
	}
	pkg := pptxOf(t, nil, nil, nil)
	pkg = replacePart(t, pkg, "ppt/presentation.xml",
		`<?xml version="1.0"?><p:presentation `+
			`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" `+
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`+
			`<p:sldIdLst>`+list.String()+`</p:sldIdLst></p:presentation>`)
	_, err := ParsePresentation(pkg, opc.Limits{})
	if err == nil {
		t.Fatal("a presentation listing too many slides was parsed")
	}
	if !strings.Contains(err.Error(), "more than") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// A slide relationship that is EXTERNAL, or that resolves to nothing, is
// skipped — and with nothing left the presentation names no slides.
func TestAnExternalOrUnresolvableSlideIsSkipped(t *testing.T) {
	for name, rel := range map[string]string{
		"external": `<Relationship Id="rId1" Type="` + relSlide +
			`" Target="https://example.com/s.xml" TargetMode="External"/>`,
		"climbing out": `<Relationship Id="rId1" Type="` + relSlide +
			`" Target="../../outside.xml"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			pkg := pptxOf(t, nil, []string{"rId1"}, nil)
			pkg = replacePart(t, pkg, "ppt/_rels/presentation.xml.rels",
				`<?xml version="1.0"?><Relationships `+
					`xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
					rel+`</Relationships>`)
			if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
				t.Error("a slide that cannot be reached was parsed as one")
			}
		})
	}
}

// A slide part the relationships name but the package does not hold.
func TestASlideThatIsNotInThePackageIsAnError(t *testing.T) {
	pkg := pptxOf(t, nil, []string{"rId1"}, map[string]string{"rId1": "slides/gone.xml"})
	if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
		t.Error("a missing slide part was parsed")
	}
}

// A slide nested past the depth ceiling.
func TestASlideNestedTooDeepIsRefused(t *testing.T) {
	deep := `<?xml version="1.0"?><p:sld xmlns:p="x" xmlns:a="y">` +
		strings.Repeat("<a:grp>", maxDepth) + strings.Repeat("</a:grp>", maxDepth) +
		`</p:sld>`
	pkg := pptxOf(t, map[string]string{"ppt/slides/slide1.xml": deep},
		[]string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
	_, err := ParsePresentation(pkg, opc.Limits{})
	if err == nil {
		t.Fatal("a slide nested past the ceiling was parsed")
	}
	if !strings.Contains(err.Error(), "nest") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// replacePart rewrites one part of a built package.
func replacePart(t *testing.T, pkg []byte, name, body string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == name {
			w.Write([]byte(body))
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ⛔ The last seven branches, each needing the truncation at an exact point:
// one token earlier and an outer walker reports it instead, and the branch
// under test never runs. The names say which walker each one is for.
func TestTheRemainingErrorPathsOnASlide(t *testing.T) {
	head := `<?xml version="1.0"?><p:sld ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody>`
	for name, body := range map[string]string{
		// A field is read through the same walker as a run, and its error
		// comes back through its own return.
		"a field":             `<a:p><a:fld id="x"><a:t>7`,
		"inside pPr":          `<a:p><a:pPr>`,
		"inside a run":        `<a:p><a:r>`,
		"inside a table cell": `<a:tbl><a:tr><a:tc>`,
	} {
		t.Run(name, func(t *testing.T) {
			pkg := pptxOf(t, map[string]string{"ppt/slides/slide1.xml": head + body},
				[]string{"rId1"}, map[string]string{"rId1": "slides/slide1.xml"})
			if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
				t.Error("a truncated slide parsed without an error")
			}
		})
	}
}

// A presentation with no p:sldIdLst at all: the reader reaches the end of the
// part without finding one, and says the presentation names no slides rather
// than walking off.
func TestAPresentationWithNoSlideListSaysSo(t *testing.T) {
	pkg := pptxOf(t, nil, nil, nil)
	pkg = replacePart(t, pkg, "ppt/presentation.xml",
		`<?xml version="1.0"?><p:presentation xmlns:p="x"><p:other/></p:presentation>`)
	_, err := ParsePresentation(pkg, opc.Limits{})
	if err == nil {
		t.Fatal("a presentation with no slide list was parsed")
	}
	if !strings.Contains(err.Error(), "no slides") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// ⛔ The two parts ParsePresentation opens before any slide — the package's own
// relationships and the presentation part — each have their own error return,
// and a single small ceiling only ever reaches the first.
func TestTheCeilingIsReportedForThePresentationsOwnParts(t *testing.T) {
	pad := "<!--" + strings.Repeat("p", 4000) + "-->"
	t.Run("no main part", func(t *testing.T) {
		pkg := pptxOf(t, nil, nil, nil)
		pkg = replacePart(t, pkg, "_rels/.rels",
			`<?xml version="1.0"?><Relationships `+
				`xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`)
		if _, err := ParsePresentation(pkg, opc.Limits{}); err == nil {
			t.Error("a package naming no main part was parsed")
		}
	})
	t.Run("the presentation part", func(t *testing.T) {
		pkg := pptxOf(t, nil, []string{"rId1"}, map[string]string{"rId1": "slides/s.xml"})
		pkg = replacePart(t, pkg, "ppt/presentation.xml",
			`<?xml version="1.0"?>`+pad+`<p:presentation xmlns:p="x"/>`)
		if _, err := ParsePresentation(pkg, opc.Limits{MaxPartBytes: 2048}); err == nil {
			t.Error("a presentation part past the ceiling was parsed")
		}
	})
}

// The presentation's own relationships are read AFTER the slide list, so they
// are the third part the ceiling can refuse and they need the padding on
// themselves.
func TestTheCeilingIsReportedForThePresentationsRelationships(t *testing.T) {
	pad := "<!--" + strings.Repeat("p", 4000) + "-->"
	pkg := pptxOf(t, nil, []string{"rId1"}, nil)
	pkg = replacePart(t, pkg, "ppt/_rels/presentation.xml.rels",
		`<?xml version="1.0"?>`+pad+`<Relationships `+
			`xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			`<Relationship Id="rId1" Type="`+relSlide+`" Target="slides/s.xml"/></Relationships>`)
	if _, err := ParsePresentation(pkg, opc.Limits{MaxPartBytes: 2048}); err == nil {
		t.Error("a relationships part past the ceiling was parsed")
	}
	// ⛔ The control: without the ceiling the same package gets past the
	// relationships and fails for the missing slide part instead, so the
	// refusal above is the ceiling and not the fixture.
	_, err := ParsePresentation(pkg, opc.Limits{})
	if err == nil {
		t.Error("the package has no slide part, so it should not parse")
	} else if strings.Contains(err.Error(), "bytes") {
		t.Errorf("without a ceiling it still failed on a size: %v", err)
	}
}
