// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
)

// withRels is docxOf plus a relationships part for the main document, so a
// hyperlink or a picture has something to point through.
func withRels(rels string) map[string]string {
	return map[string]string{
		"word/_rels/document.xml.rels": `<?xml version="1.0"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			rels + `</Relationships>`,
		"word/media/image1.png": "\x89PNG\r\n\x1a\n not really",
	}
}

// ⛔ A picture is named by a RELATIONSHIP id on an a:blip, and the id is
// NAMESPACED — r:embed. A reader matching a plain "embed" would also match one
// on some other element, and one matching plain "id" would take the
// decorative id that every shape carries.
func TestAPictureIsFoundThroughItsRelationship(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:drawing>`+
		`<wp:docPr xmlns:wp="x" id="1" name="The lamp" descr="A worn scanner lamp"/>`+
		`<a:blip xmlns:a="y" r:embed="rId5"/>`+
		`</w:drawing></w:r></w:p>`,
		withRels(`<Relationship Id="rId5" Type="`+RelImage+`" Target="media/image1.png"/>`))
	if !strings.Contains(got, "[img word/media/image1.png]") {
		t.Errorf("the picture came out as %q", got)
	}
}

// The alt text and the title are ATTRIBUTES of docPr, not elements — which is
// the opposite of ODF, where svg:title and svg:desc are the frame's children.
func TestAPictureKeepsItsAlternativeText(t *testing.T) {
	d, err := ParseWord(docxOf(t, `<w:p><w:r><w:drawing>`+
		`<wp:docPr xmlns:wp="x" name="The lamp" descr="A worn scanner lamp"/>`+
		`<a:blip xmlns:a="y" r:embed="rId5"/>`+
		`</w:drawing></w:r></w:p>`,
		withRels(`<Relationship Id="rId5" Type="`+RelImage+`" Target="media/image1.png"/>`)), opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	img := firstImage(t, d)
	if img.Alt != "A worn scanner lamp" {
		t.Errorf("alt = %q", img.Alt)
	}
	if img.Title != "The lamp" {
		t.Errorf("title = %q", img.Title)
	}
}

// The older shape, which Word still writes for a pasted picture: w:pict with a
// VML v:imagedata whose r:id names the relationship.
func TestTheOlderPictureShapeIsRead(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:pict>`+
		`<v:imagedata xmlns:v="urn:vml" r:id="rId5"/>`+
		`</w:pict></w:r></w:p>`,
		withRels(`<Relationship Id="rId5" Type="`+RelImage+`" Target="media/image1.png"/>`))
	if !strings.Contains(got, "[img word/media/image1.png]") {
		t.Errorf("a VML picture came out as %q", got)
	}
}

// A picture whose relationship is EXTERNAL keeps the URL, because that is what
// a linked rather than embedded image is.
func TestAnExternalPictureKeepsItsURL(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:drawing>`+
		`<a:blip xmlns:a="y" r:embed="rId6"/></w:drawing></w:r></w:p>`,
		withRels(`<Relationship Id="rId6" Type="`+RelImage+
			`" Target="https://example.com/p.png" TargetMode="External"/>`))
	if !strings.Contains(got, "[img https://example.com/p.png]") {
		t.Errorf("an external picture came out as %q", got)
	}
}

// ⛔ A drawing that names no picture emits NOTHING, rather than an image with
// an empty URL. A chart, a text box and a shape are all w:drawing, and an
// empty image reference reaches a PDF as a broken box.
func TestADrawingWithNoPictureEmitsNothing(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:t>before</w:t><w:drawing>`+
		`<wps:txbx xmlns:wps="z"/></w:drawing><w:t>after</w:t></w:r></w:p>`, nil)
	if strings.Contains(got, "[img ") {
		t.Errorf("an empty image reference was emitted: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("the text around it was lost: %q", got)
	}
}

// A blip naming a relationship the file never declares is the same case: no
// picture, and the words around it survive.
func TestAPictureWithAnUnknownRelationshipIsDropped(t *testing.T) {
	got := parseBody(t, `<w:p><w:r><w:t>x</w:t><w:drawing>`+
		`<a:blip xmlns:a="y" r:embed="rIdNope"/></w:drawing></w:r></w:p>`, nil)
	if strings.Contains(got, "[img ") {
		t.Errorf("got %q", got)
	}
}
