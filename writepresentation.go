// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-ooxml/opc"
)

// The content types and relationship types a .pptx is made of. The PART
// requirements are ECMA-376 Part 1 §13.3; the namespaces are the transitional
// ones, which is what PowerPoint, LibreOffice and marp-cli all write — Part 1's
// own tables print the strict purl.oclc.org names, which PowerPoint does not
// produce and older readers do not know.
const (
	ctPresentation = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	ctSlide        = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
	ctSlideLayout  = "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"
	ctSlideMaster  = "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"
	ctNotesSlide   = "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"
	ctNotesMaster  = "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"
	ctPresProps    = "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"
	ctViewProps    = "application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"
	ctTableStyles  = "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"
	ctTheme        = "application/vnd.openxmlformats-officedocument.theme+xml"
	ctCore         = "application/vnd.openxmlformats-package.core-properties+xml"

	relDoc         = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	relSlideLayout = relDoc + "slideLayout"
	relSlideMaster = relDoc + "slideMaster"
	relNotesSlide  = relDoc + "notesSlide"
	relNotesMaster = relDoc + "notesMaster"
	relPresProps   = relDoc + "presProps"
	relViewProps   = relDoc + "viewProps"
	relTableStyles = relDoc + "tableStyles"
	relTheme       = relDoc + "theme"
	relImage       = relDoc + "image"

	nsP = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsA = "http://schemas.openxmlformats.org/drawingml/2006/main"
)

// The slide size is ST_SlideSizeCoordinate: 914400 to 51206400 EMU, one inch
// to 56 inches, in pml.xsd. PowerPoint refuses a deck outside it.
const (
	minSlideEMU = 914400
	maxSlideEMU = 51206400
)

// The bounds on what WritePresentation accepts. They are var rather than const
// so that a test can lower them, as opc's are: a test that had to build 64 MiB
// to see a refusal would be a test nobody runs.
var (

	// maxWriteSlides keeps what is written readable by [ParsePresentation]
	// under the default [opc.Limits]: a slide costs up to five ZIP entries
	// (the slide, its picture, its notes and two relationship parts), and
	// 1000 of them stay well inside the 8192 entries a reader allows.
	maxWriteSlides = 1000

	// maxImageBytes is one picture, and maxImagesBytes all of them. They
	// are the default opc ceilings for one part (64 MiB) and, with room for
	// the rest of the package, for the whole file (256 MiB): a writer that
	// produced what its own reader refuses would be a writer of dead files.
	maxImageBytes  = 64 << 20
	maxImagesBytes = int64(192 << 20)

	// maxTextBytes bounds each piece of text: the notes of one slide, its
	// name, and each document property.
	maxTextBytes = 1 << 20
)

// A Presentation is a deck to write as a .pptx: one full-slide picture per
// slide, with its speaker notes.
//
// Width and Height are the slide size in EMU (English Metric Units, 914400 to
// the inch). A CSS pixel is 9525 EMU, so a 1280×720 deck is 12192000×6858000,
// which is PowerPoint's own 16:9.
type Presentation struct {
	Width, Height int64
	Slides        []PresentationSlide

	// The document properties, written to docProps/core.xml when set: dc:title,
	// dc:creator, dc:subject and cp:keywords.
	Title, Author, Subject, Keywords string

	// Created is written as both dcterms:created and dcterms:modified. The
	// zero time writes neither, which is what makes the output the same
	// bytes for the same input: a writer that stamped the clock could not be
	// diffed, cached or reproduced.
	Created time.Time

	// Images are the pictures editable slides' shapes show (Shape.Image),
	// each stored once in the package however many slides show it.
	Images []Image

	// Fonts are typefaces embedded in the deck, for viewers that do not
	// have them (p:embeddedFontLst, §19.2.1.10). Embed only fonts whose
	// licence allows it.
	Fonts []Font
}

// A PresentationSlide is one slide: a picture that covers it, or editable
// shapes, or both (the picture at the back), and its notes.
type PresentationSlide struct {
	// Image is the picture, stretched over the whole slide; render it at the
	// slide's aspect ratio. ImageType is "png" or "jpeg", and is checked
	// against the image's own signature. It may be left out when the slide
	// has Shapes or a Background.
	Image     []byte
	ImageType string

	// Background is the slide's background (p:bg); nil leaves the master's,
	// which is none.
	Background *Fill

	// Shapes are the slide's editable content, back to front.
	Shapes []Shape

	// Notes are the speaker notes, one paragraph per line. Empty writes no
	// notes slide, as ECMA-376 §13.3.5 asks: one per slide THAT HAS notes.
	Notes string

	// Name names the slide (p:cSld name) and is the picture's alternative
	// text, which is what a screen reader says for a slide that is a picture.
	// Empty gives "Slide N".
	Name string
}

// WritePresentation writes a deck as a PresentationML package (.pptx).
//
// A slide is a picture (what marp-cli's --pptx writes), editable shapes
// (Shapes: text boxes, filled and gradient shapes, pictures — set in fonts
// the deck can embed, so that it is the same slide in every viewer), or both.
// [ParsePresentation] reads back the text boxes' words; a picture's are in
// the picture.
//
// ⛔ The output is deterministic: the same Presentation gives the same bytes.
// No clock is read (see [Presentation.Created]) and every id is a position.
func WritePresentation(p Presentation) ([]byte, error) {
	if err := checkPresentation(p); err != nil {
		return nil, err
	}
	n := len(p.Slides)
	parts := make([]opc.NewPart, 0, 10+3*n)
	rels := map[string][]opc.Rel{
		"": {{ID: "rId1", Type: opc.RelOfficeDocument, Target: "ppt/presentation.xml"}},
	}
	if core := coreXML(p); core != nil {
		parts = append(parts, opc.NewPart{Name: "docProps/core.xml", ContentType: ctCore, Data: core})
		rels[""] = append(rels[""], opc.Rel{ID: "rId2", Type: opc.RelCoreProperties, Target: "docProps/core.xml"})
	}

	// presentation.xml's relationships: the master first, the slides after
	// it, then the parts §13.3.6 hangs off the presentation.
	presRels := []opc.Rel{{ID: "rId1", Type: relSlideMaster, Target: "slideMasters/slideMaster1.xml"}}
	for i := range p.Slides {
		presRels = append(presRels, opc.Rel{
			ID: "rId" + strconv.Itoa(i+2), Type: relSlide,
			Target: "slides/slide" + strconv.Itoa(i+1) + ".xml",
		})
	}
	next := n + 2
	// The embedded fonts: one part per face, related from the presentation
	// and named in its embeddedFontLst (§19.2.1.9).
	var fontRefs [][4]string
	for i, f := range p.Fonts {
		var ref [4]string
		for j, data := range [][]byte{f.Regular, f.Bold, f.Italic, f.BoldItal} {
			if data == nil {
				continue
			}
			name := "fonts/font" + strconv.Itoa(i+1) + "_" + strconv.Itoa(j) + ".ttf"
			parts = append(parts, opc.NewPart{Name: "ppt/" + name, ContentType: ctFontData, Data: data})
			ref[j] = "rId" + strconv.Itoa(next)
			presRels = append(presRels, opc.Rel{ID: ref[j], Type: relFont, Target: name})
			next++
		}
		fontRefs = append(fontRefs, ref)
	}
	for i, img := range p.Images {
		parts = append(parts, opc.NewPart{Name: "ppt/" + imageName(i+1, img), ContentType: "image/" + img.Type, Data: img.Data})
	}
	notesRel := next
	for _, r := range []struct{ typ, target string }{
		{relNotesMaster, "notesMasters/notesMaster1.xml"},
		{relPresProps, "presProps.xml"},
		{relViewProps, "viewProps.xml"},
		{relTheme, "theme/theme1.xml"},
		{relTableStyles, "tableStyles.xml"},
	} {
		presRels = append(presRels, opc.Rel{ID: "rId" + strconv.Itoa(next), Type: r.typ, Target: r.target})
		next++
	}
	rels["ppt/presentation.xml"] = presRels

	parts = append(parts,
		opc.NewPart{Name: "ppt/presentation.xml", ContentType: ctPresentation, Data: presentationXML(p, notesRel, fontRefs)},
		opc.NewPart{Name: "ppt/presProps.xml", ContentType: ctPresProps, Data: []byte(xml.Header + `<p:presentationPr xmlns:p="` + nsP + `"/>`)},
		opc.NewPart{Name: "ppt/viewProps.xml", ContentType: ctViewProps, Data: []byte(xml.Header + `<p:viewPr xmlns:p="` + nsP + `"/>`)},
		// def is required, and this GUID is PowerPoint's own "no style" —
		// it is what PowerPoint and marp-cli both write here.
		opc.NewPart{Name: "ppt/tableStyles.xml", ContentType: ctTableStyles, Data: []byte(xml.Header + `<a:tblStyleLst xmlns:a="` + nsA + `" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`)},
		opc.NewPart{Name: "ppt/theme/theme1.xml", ContentType: ctTheme, Data: []byte(themeXML)},
		opc.NewPart{Name: "ppt/slideMasters/slideMaster1.xml", ContentType: ctSlideMaster, Data: []byte(slideMasterXML)},
		opc.NewPart{Name: "ppt/slideLayouts/slideLayout1.xml", ContentType: ctSlideLayout, Data: []byte(slideLayoutXML)},
		opc.NewPart{Name: "ppt/notesMasters/notesMaster1.xml", ContentType: ctNotesMaster, Data: []byte(notesMasterXML)},
	)
	rels["ppt/slideMasters/slideMaster1.xml"] = []opc.Rel{
		{ID: "rId1", Type: relSlideLayout, Target: "../slideLayouts/slideLayout1.xml"},
		{ID: "rId2", Type: relTheme, Target: "../theme/theme1.xml"},
	}
	rels["ppt/slideLayouts/slideLayout1.xml"] = []opc.Rel{
		{ID: "rId1", Type: relSlideMaster, Target: "../slideMasters/slideMaster1.xml"},
	}
	rels["ppt/notesMasters/notesMaster1.xml"] = []opc.Rel{
		{ID: "rId1", Type: relTheme, Target: "../theme/theme1.xml"},
	}

	for i, s := range p.Slides {
		num := strconv.Itoa(i + 1)
		slide := "ppt/slides/slide" + num + ".xml"
		name := s.Name
		if name == "" {
			name = "Slide " + num
		}
		slideRels := []opc.Rel{{ID: "rId1", Type: relSlideLayout, Target: "../slideLayouts/slideLayout1.xml"}}
		picRel := ""
		if s.Image != nil {
			media := "media/image" + num + "." + s.ImageType
			parts = append(parts, opc.NewPart{Name: "ppt/" + media, ContentType: "image/" + s.ImageType, Data: s.Image})
			picRel = "rId" + strconv.Itoa(len(slideRels)+1)
			slideRels = append(slideRels, opc.Rel{ID: picRel, Type: relImage, Target: "../" + media})
		}
		if s.Notes != "" {
			notes := "ppt/notesSlides/notesSlide" + num + ".xml"
			parts = append(parts, opc.NewPart{Name: notes, ContentType: ctNotesSlide, Data: notesXML(s.Notes)})
			slideRels = append(slideRels, opc.Rel{ID: "rId" + strconv.Itoa(len(slideRels)+1), Type: relNotesSlide, Target: "../notesSlides/notesSlide" + num + ".xml"})
			rels[notes] = []opc.Rel{
				{ID: "rId1", Type: relNotesMaster, Target: "../notesMasters/notesMaster1.xml"},
				{ID: "rId2", Type: relSlide, Target: "../slides/slide" + num + ".xml"},
			}
		}
		sp := newSlideParts(len(slideRels) + 1)
		parts = append(parts, opc.NewPart{Name: slide, ContentType: ctSlide, Data: slideXML(p, s, name, picRel, sp)})
		for _, r := range sp.order {
			rel := opc.Rel{ID: r.id, Type: r.typ, Target: r.target}
			if r.external {
				rel.External = true
			}
			slideRels = append(slideRels, rel)
		}
		rels[slide] = slideRels
	}
	return opc.Build(parts, rels)
}

// The embedded fonts' content and relationship types. The data is the font
// file itself, which §15.2.13 types application/x-font-ttf (Open Font
// Format); application/x-fontdata would announce Embedded OpenType.
const (
	ctFontData = "application/x-font-ttf"
	relFont    = relDoc + "font"
)

// checkPresentation refuses what would make a file PowerPoint refuses, or one
// larger than the bounds above.
func checkPresentation(p Presentation) error {
	for _, d := range []struct {
		what string
		v    int64
	}{{"width", p.Width}, {"height", p.Height}} {
		if d.v < minSlideEMU || d.v > maxSlideEMU {
			return fmt.Errorf("ooxml: a slide %s of %d EMU is outside the %d to %d the standard allows", d.what, d.v, minSlideEMU, maxSlideEMU)
		}
	}
	if len(p.Slides) == 0 {
		return fmt.Errorf("ooxml: no slides to write")
	}
	if len(p.Slides) > maxWriteSlides {
		return fmt.Errorf("ooxml: %d slides is past the %d this writes", len(p.Slides), maxWriteSlides)
	}
	for _, t := range []string{p.Title, p.Author, p.Subject, p.Keywords} {
		if len(t) > maxTextBytes {
			return fmt.Errorf("ooxml: a document property of %d bytes is past the %d allowed", len(t), maxTextBytes)
		}
	}
	var total int64
	for i, s := range p.Slides {
		// A slide with editable content may do without a picture; one with
		// neither is a blank the caller did not mean.
		if s.Image != nil || (len(s.Shapes) == 0 && s.Background == nil) {
			if err := checkImage(s); err != nil {
				return fmt.Errorf("ooxml: slide %d: %w", i+1, err)
			}
		}
		if len(s.Notes) > maxTextBytes || len(s.Name) > maxTextBytes {
			return fmt.Errorf("ooxml: slide %d: its notes or name are past the %d bytes allowed", i+1, maxTextBytes)
		}
		total += int64(len(s.Image))
		if total > maxImagesBytes {
			return fmt.Errorf("ooxml: the pictures come to more than the %d bytes allowed", maxImagesBytes)
		}
	}
	return checkShapes(p)
}

// checkImage refuses a picture that is not what its type says.
//
// ⛔ The type becomes the part's extension and its content type, so a JPEG
// called "png" makes a package that says one thing and holds another — which
// a viewer renders as a blank slide, if it opens the file at all.
func checkImage(s PresentationSlide) error {
	var magic string
	switch s.ImageType {
	case "png":
		magic = "\x89PNG\r\n\x1a\n"
	case "jpeg":
		magic = "\xff\xd8\xff"
	default:
		return fmt.Errorf("image type %q is not png or jpeg", s.ImageType)
	}
	if len(s.Image) > maxImageBytes {
		return fmt.Errorf("a picture of %d bytes is past the %d allowed", len(s.Image), maxImageBytes)
	}
	if !bytes.HasPrefix(s.Image, []byte(magic)) {
		return fmt.Errorf("the picture is not a %s", s.ImageType)
	}
	return nil
}

// presentationXML writes ppt/presentation.xml. notesRel is the relationship
// number of the notes master, which comes after the slides'.
//
// ⛔ The ids are the standard's ranges, not counters from 1: a slide id is
// 256 or more (ST_SlideId), and a master or layout id 2^31 or more
// (ST_SlideMasterId, ST_SlideLayoutId) — and the master's and its layouts'
// share one space, which is why the layout is 2147483649.
func presentationXML(p Presentation, notesRel int, fontRefs [][4]string) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<p:presentation xmlns:a=%q xmlns:r=%q xmlns:p=%q`, nsA, nsR, nsP)
	if len(p.Fonts) > 0 {
		b.WriteString(` embedTrueTypeFonts="1"`)
	}
	b.WriteString(`>`)
	b.WriteString(`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`)
	fmt.Fprintf(&b, `<p:notesMasterIdLst><p:notesMasterId r:id="rId%d"/></p:notesMasterIdLst>`, notesRel)
	b.WriteString(`<p:sldIdLst>`)
	for i := range p.Slides {
		fmt.Fprintf(&b, `<p:sldId id="%d" r:id="rId%d"/>`, 256+i, i+2)
	}
	b.WriteString(`</p:sldIdLst>`)
	fmt.Fprintf(&b, `<p:sldSz cx="%d" cy="%d"/>`, p.Width, p.Height)
	// The notes page is a portrait sheet of paper whatever the slide is:
	// 7.5 × 10 inches, as PowerPoint writes it.
	b.WriteString(`<p:notesSz cx="6858000" cy="9144000"/>`)
	if len(p.Fonts) > 0 {
		b.WriteString(`<p:embeddedFontLst>`)
		for i, f := range p.Fonts {
			b.WriteString(`<p:embeddedFont><p:font typeface="`)
			esc(&b, f.Typeface)
			b.WriteString(`"/>`)
			for j, el := range []string{"regular", "bold", "italic", "boldItalic"} {
				if id := fontRefs[i][j]; id != "" {
					b.WriteString(`<p:` + el + ` r:id="` + id + `"/>`)
				}
			}
			b.WriteString(`</p:embeddedFont>`)
		}
		b.WriteString(`</p:embeddedFontLst>`)
	}
	b.WriteString(`</p:presentation>`)
	return b.Bytes()
}

// slideXML writes one slide: its background, the picture from corner to
// corner when it has one (picRel), then its shapes.
func slideXML(p Presentation, s PresentationSlide, name, picRel string, sp *slideParts) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<p:sld xmlns:a=%q xmlns:r=%q xmlns:p=%q><p:cSld name="`, nsA, nsR, nsP)
	esc(&b, name)
	b.WriteString(`">`)
	writeBackground(&b, s.Background)
	b.WriteString(`<p:spTree>`)
	b.WriteString(emptyGroup)
	target := func(i int) string { return "../" + imageName(i, p.Images[i-1]) }
	first := 2
	if picRel != "" {
		first = 3 // the shapes' ids follow the picture's
		b.WriteString(`<p:pic><p:nvPicPr><p:cNvPr id="2" name="Picture 1" descr="`)
		esc(&b, name)
		b.WriteString(`"/><p:cNvPicPr><a:picLocks noGrp="1" noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`)
		b.WriteString(`<p:blipFill><a:blip r:embed="` + picRel + `"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>`)
		fmt.Fprintf(&b, `<p:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`, p.Width, p.Height)
	}
	writeShapes(&b, s.Shapes, first, sp, target)
	b.WriteString(`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
	return b.Bytes()
}

// notesXML writes one notes slide: the slide's image and a body placeholder
// holding the notes, a paragraph per line. The placeholders are what tie the
// shapes to the notes master's; a body shape that is not one is a text box
// PowerPoint does not show in the notes pane.
func notesXML(notes string) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<p:notes xmlns:a=%q xmlns:r=%q xmlns:p=%q><p:cSld><p:spTree>`, nsA, nsR, nsP)
	b.WriteString(emptyGroup)
	b.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>`)
	b.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>`)
	for _, line := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		if line == "" {
			b.WriteString(`<a:p/>`)
			continue
		}
		b.WriteString(`<a:p><a:r><a:t>`)
		esc(&b, line)
		b.WriteString(`</a:t></a:r></a:p>`)
	}
	b.WriteString(`</p:txBody></p:sp></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`)
	return b.Bytes()
}

// coreXML writes docProps/core.xml, or nil when there is nothing to say.
func coreXML(p Presentation) []byte {
	if p.Title == "" && p.Author == "" && p.Subject == "" && p.Keywords == "" && p.Created.IsZero() {
		return nil
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`)
	for _, f := range []struct{ tag, v string }{
		{"dc:title", p.Title}, {"dc:subject", p.Subject}, {"dc:creator", p.Author}, {"cp:keywords", p.Keywords},
	} {
		if f.v == "" {
			continue
		}
		b.WriteString("<" + f.tag + ">")
		esc(&b, f.v)
		b.WriteString("</" + f.tag + ">")
	}
	if !p.Created.IsZero() {
		// W3CDTF in UTC, with the xsi:type ECMA-376 Part 2 requires on both.
		t := p.Created.UTC().Format("2006-01-02T15:04:05Z")
		for _, tag := range []string{"dcterms:created", "dcterms:modified"} {
			fmt.Fprintf(&b, `<%s xsi:type="dcterms:W3CDTF">%s</%s>`, tag, t, tag)
		}
	}
	b.WriteString(`</cp:coreProperties>`)
	return b.Bytes()
}

// emptyGroup is the shape tree's own group properties, which every spTree
// starts with.
const emptyGroup = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

// clrMap maps the theme's colours to the names slides use; a master must have
// one (EG_TopLevelSlide).
const clrMap = `<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`

const pptHead = xml.Header

// slideMasterXML is the one master. Its layout list names the one layout, with
// the id after the master's own (see presentationXML).
const slideMasterXML = pptHead + `<p:sldMaster xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `"><p:cSld><p:spTree>` + emptyGroup + `</p:spTree></p:cSld>` + clrMap +
	`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst></p:sldMaster>`

// slideLayoutXML is the one layout, blank: the picture is the slide.
const slideLayoutXML = pptHead + `<p:sldLayout xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `" type="blank" preserve="1"><p:cSld name="Blank"><p:spTree>` + emptyGroup +
	`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

// notesMasterXML is the notes master: where a notes page puts the slide's
// image and the notes, on the 7.5 × 10 inch page presentationXML declares.
const notesMasterXML = pptHead + `<p:notesMaster xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `"><p:cSld><p:spTree>` + emptyGroup +
	`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldImg" idx="2"/></p:nvPr></p:nvSpPr>` +
	`<p:spPr><a:xfrm><a:off x="685800" y="1143000"/><a:ext cx="5486400" cy="3086100"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr></p:sp>` +
	`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" sz="quarter" idx="1"/></p:nvPr></p:nvSpPr>` +
	`<p:spPr><a:xfrm><a:off x="685800" y="4400550"/><a:ext cx="5486400" cy="3600450"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>` +
	`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
	`</p:spTree></p:cSld>` + clrMap +
	`<p:notesStyle><a:lvl1pPr marL="0" algn="l"><a:defRPr sz="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/></a:defRPr></a:lvl1pPr></p:notesStyle></p:notesMaster>`

// themeXML is the smallest theme the schema allows: twelve colours, a major
// and a minor font, and three of each style list (CT_StyleMatrix asks for at
// least three). Nothing on a slide uses it — the slides are pictures — but a
// master and a notes master must each have one.
const themeXML = pptHead + `<a:theme xmlns:a="` + nsA + `" name="Office Theme"><a:themeElements>` +
	`<a:clrScheme name="Office"><a:dk1><a:srgbClr val="000000"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>` +
	`<a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2><a:accent3><a:srgbClr val="A5A5A5"/></a:accent3><a:accent4><a:srgbClr val="FFC000"/></a:accent4><a:accent5><a:srgbClr val="5B9BD5"/></a:accent5><a:accent6><a:srgbClr val="70AD47"/></a:accent6>` +
	`<a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme>` +
	`<a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont><a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>` +
	`<a:fmtScheme name="Office"><a:fillStyleLst>` + solid3 + `</a:fillStyleLst><a:lnStyleLst>` + line3 + `</a:lnStyleLst>` +
	`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
	`<a:bgFillStyleLst>` + solid3 + `</a:bgFillStyleLst></a:fmtScheme></a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`

const solid3 = `<a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>`

const line3 = `<a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>`
