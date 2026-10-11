// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
)

// ⛔ Every walker in word.go reads from the same decoder, and each has its own
// error return for a token that does not arrive. A reader that returned nil
// there would report a TRUNCATED document as a complete one — the worst
// direction for a converter, because the output opens and looks fine.
//
// One case per walker, named for the walker it reaches, so a failure says
// which one stopped checking.
func TestTruncationIsAnErrorWhereverItHappens(t *testing.T) {
	for name, body := range map[string]string{
		"blocks":          `<w:p/><w:tb`,
		"paragraph":       `<w:p><w:r`,
		"paragraph props": `<w:p><w:pPr><w:num`,
		"numPr":           `<w:p><w:pPr><w:numPr><w:numId w:val="1"`,
		"run":             `<w:p><w:r><w:t>x</w:t><w:b`,
		"run props":       `<w:p><w:r><w:rPr><w:b`,
		"flat text":       `<w:p><w:r><w:t>unclosed`,
		"hyperlink":       `<w:p><w:hyperlink r:id="r"><w:r`,
		"insertion":       `<w:p><w:ins><w:r`,
		"drawing":         `<w:p><w:r><w:drawing><a:blip xmlns:a="y"`,
		"table":           `<w:tbl><w:tr`,
		"row":             `<w:tbl><w:tr><w:tc`,
		"body":            `<w:p>`,
	} {
		t.Run(name, func(t *testing.T) {
			// ⛔ The fixture is built WITHOUT closing the body, so the
			// document really is truncated rather than merely odd.
			pkg := docxOf(t, "", map[string]string{
				"word/document.xml": `<?xml version="1.0"?><w:document ` +
					`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
					`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
					`<w:body>` + body,
			})
			if _, err := ParseWord(pkg, opc.Limits{}); err == nil {
				t.Error("a truncated document parsed without an error")
			}
		})
	}
}

// The numbering part is read separately, so its own truncation and its own
// ceiling are separate paths.
func TestABrokenNumberingPartIsAnError(t *testing.T) {
	pkg := docxOf(t, `<w:p/>`, map[string]string{
		"word/numbering.xml": `<?xml version="1.0"?><w:numbering xmlns:w="x"><w:num`,
	})
	if _, err := ParseWord(pkg, opc.Limits{}); err == nil {
		t.Error("a truncated numbering part parsed without an error")
	}
}

// ⛔ The numbering part is found BESIDE the main part, through its own
// relationship — not at the fixed path word/numbering.xml. A document whose
// main part is somewhere else has its numbering somewhere else too.
func TestTheNumberingPartIsFoundThroughItsRelationship(t *testing.T) {
	const relNumbering = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	numbering := `<?xml version="1.0"?><w:numbering ` +
		`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:abstractNum w:abstractNumId="1"><w:lvl w:ilvl="0">` +
		`<w:numFmt w:val="decimal"/></w:lvl></w:abstractNum>` +
		`<w:num w:numId="1"><w:abstractNumId w:val="1"/></w:num></w:numbering>`
	got := parseBody(t,
		`<w:p><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr>`+
			`<w:r><w:t>one</w:t></w:r></w:p>`,
		map[string]string{
			// Deliberately NOT word/numbering.xml.
			"word/lists.xml": numbering,
			"word/_rels/document.xml.rels": `<?xml version="1.0"?>` +
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
				`<Relationship Id="rN" Type="` + relNumbering + `" Target="lists.xml"/>` +
				`</Relationships>`,
		})
	if got != "1. one\n" {
		t.Errorf("the numbering was not found at its relationship: %q", got)
	}
}

// ⛔ The SECOND kind of truncation: inside an element the reader is SKIPPING.
// Every walker calls dec.Skip on what it does not want, and Skip has its own
// error — a document that ends in the middle of an element nobody reads is
// still a truncated document, and reporting it as complete is the worst
// direction for a converter because the output opens and looks fine.
//
// The table above truncates where the reader is LOOKING; this one truncates
// where it is looking away.
func TestTruncationInsideSomethingSkippedIsAlsoAnError(t *testing.T) {
	for name, body := range map[string]string{
		"a block nobody reads":   `<w:sectPr><w:inner`,
		"inside a paragraph":     `<w:p><w:unknown><w:inner`,
		"a paragraph property":   `<w:p><w:pPr><w:unknown><w:inner`,
		"a style element":        `<w:p><w:pPr><w:pStyle w:val="Normal"><w:inner`,
		"an outline level":       `<w:p><w:pPr><w:outlineLvl w:val="0"><w:inner`,
		"inside numPr":           `<w:p><w:pPr><w:numPr><w:ilvl><w:inner`,
		"a run property":         `<w:p><w:r><w:rPr><w:b><w:inner`,
		"a line break":           `<w:p><w:r><w:br><w:inner`,
		"a tab":                  `<w:p><w:r><w:tab><w:inner`,
		"a field's instructions": `<w:p><w:r><w:instrText><w:inner`,
		"inside a run":           `<w:p><w:r><w:unknown><w:inner`,
		"inside a drawing":       `<w:p><w:r><w:drawing><a:blip xmlns:a="y"><w:inner`,
		"inside a hyperlink":     `<w:p><w:hyperlink><w:unknown><w:inner`,
		"inside an insertion":    `<w:p><w:ins><w:unknown><w:inner`,
		"inside a table":         `<w:tbl><w:unknown><w:inner`,
		"inside a row":           `<w:tbl><w:tr><w:unknown><w:inner`,
	} {
		t.Run(name, func(t *testing.T) {
			pkg := docxOf(t, "", map[string]string{
				"word/document.xml": `<?xml version="1.0"?><w:document ` +
					`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
					`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
					`<w:body>` + body,
			})
			if _, err := ParseWord(pkg, opc.Limits{}); err == nil {
				t.Error("a truncated document parsed without an error")
			}
		})
	}
}

// ⛔ The ceiling can refuse any ONE of the four parts this reader opens, and
// each refusal comes back through a different error return. A single
// MaxPartBytes test only ever reaches the first of them — _rels/.rels, read by
// Main — so the other three need a package where the part under test is the
// large one.
func TestTheCeilingIsReportedForEachPartTheReaderOpens(t *testing.T) {
	pad := func(n int) string { return "<!--" + strings.Repeat("p", n) + "-->" }
	const relNumbering = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	docXML := func(extra string) string {
		return `<?xml version="1.0"?><w:document ` +
			`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			extra + `<w:body><w:p/></w:body></w:document>`
	}
	for name, parts := range map[string]map[string]string{
		"the main part": {
			"word/document.xml": docXML(pad(4000)),
		},
		"the relationships of the main part": {
			"word/document.xml": docXML(""),
			"word/_rels/document.xml.rels": `<?xml version="1.0"?>` + pad(4000) +
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`,
		},
		"the numbering part": {
			"word/document.xml":  docXML(""),
			"word/numbering.xml": `<?xml version="1.0"?>` + pad(4000) + `<w:numbering xmlns:w="x"/>`,
		},
		"the numbering part named by a relationship": {
			"word/document.xml": docXML(""),
			"word/lists.xml":    `<?xml version="1.0"?>` + pad(4000) + `<w:numbering xmlns:w="x"/>`,
			"word/_rels/document.xml.rels": `<?xml version="1.0"?>` +
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
				`<Relationship Id="rN" Type="` + relNumbering + `" Target="lists.xml"/></Relationships>`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			pkg := docxOf(t, "", parts)
			// 2 KiB: large enough for every other part, small enough to
			// refuse the padded one.
			if _, err := ParseWord(pkg, opc.Limits{MaxPartBytes: 2048}); err == nil {
				t.Error("a part past the ceiling was read")
			}
			// ⛔ The control: without the ceiling the same package parses, so
			// the refusal above is the ceiling and not the fixture.
			if _, err := ParseWord(pkg, opc.Limits{}); err != nil {
				t.Errorf("the same package with no ceiling: %v", err)
			}
		})
	}
}

// A hyperlink whose relationship is INTERNAL points at a part of the package —
// a document linking to another document inside the same file — and the target
// resolves to a part name rather than staying a URL.
func TestAnInternalHyperlinkResolvesToAPartName(t *testing.T) {
	got := parseBody(t, `<w:p><w:hyperlink r:id="rId7">`+
		`<w:r><w:t>the other part</w:t></w:r></w:hyperlink></w:p>`,
		map[string]string{
			"word/other.xml": `<?xml version="1.0"?><x/>`,
			"word/_rels/document.xml.rels": `<?xml version="1.0"?>` +
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
				`<Relationship Id="rId7" Type="` + RelHyperlink + `" Target="other.xml"/></Relationships>`,
		})
	if got != "[the other part](word/other.xml)\n" {
		t.Errorf("an internal hyperlink came out as %q", got)
	}
}

// ⛔ The four places a walker receives an error from the walker it CALLED.
// Each needs the inner walker to be entered and then fail, which a truncation
// one token earlier does not do: it fails in the caller instead, and the
// branch under test never runs. These were the last four gaps in the profile,
// and each took a fixture a token longer than the obvious one.
func TestAnErrorFromAnInnerWalkerReachesTheOuterOne(t *testing.T) {
	for name, body := range map[string]string{
		// No w:body at all, and truncated: body() is still looking when the
		// tokens stop. (A complete document with no body is the other branch,
		// and says so.)
		"looking for the body":  `NOBODY<w:x`,
		"a run in a hyperlink":  `<w:p><w:hyperlink r:id="r"><w:r><w:t>x`,
		"a run in an insertion": `<w:p><w:ins><w:r><w:t>x`,
		"blocks in a cell":      `<w:tbl><w:tr><w:tc><w:p><w:r><w:t>x`,
	} {
		t.Run(name, func(t *testing.T) {
			doc := `<?xml version="1.0"?><w:document ` +
				`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
				`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`
			if body != "NOBODY<w:x" {
				doc += `<w:body>` + body
			} else {
				doc += `<w:x`
			}
			if _, err := ParseWord(docxOf(t, "", map[string]string{
				"word/document.xml": doc,
			}), opc.Limits{}); err == nil {
				t.Error("a truncated document parsed without an error")
			}
		})
	}
}
