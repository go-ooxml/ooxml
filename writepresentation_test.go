// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// pictures are real PNG and JPEG files, made here, so that a reader that
// decodes them — LibreOffice does — has something to decode.
func pictures(t testing.TB) (pngData, jpegData []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 9))
	for y := 0; y < 9; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 16), uint8(y * 28), 128, 255})
		}
	}
	var p, j bytes.Buffer
	if err := png.Encode(&p, img); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&j, img, nil); err != nil {
		t.Fatal(err)
	}
	return p.Bytes(), j.Bytes()
}

// deck is a three-slide deck at marp's 16:9: a PNG with two lines of notes, a
// JPEG with none, and a PNG whose notes and name need escaping.
func deck(t testing.TB) Presentation {
	p, j := pictures(t)
	return Presentation{
		Width: 1280 * 9525, Height: 720 * 9525,
		Title: "The Deck", Author: "Ada & Bob", Subject: "A test", Keywords: "one, two",
		Slides: []PresentationSlide{
			{Image: p, ImageType: "png", Notes: "first note\r\nsecond line", Name: "Opening"},
			{Image: j, ImageType: "jpeg"},
			{Image: p, ImageType: "png", Notes: "a < b & \"c\"\n\nafter a blank", Name: "x <y>"},
		},
	}
}

func writeDeck(t testing.TB, p Presentation) []byte {
	t.Helper()
	b, err := WritePresentation(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The control is the reader that was here first: every slide comes back, in
// order, and the words are not there because they are in the pictures.
func TestWrittenPresentationReadsBack(t *testing.T) {
	doc, err := ParsePresentation(writeDeck(t, deck(t)), opc.Limits{})
	if err != nil {
		t.Fatalf("what was written could not be read: %v", err)
	}
	var headings []string
	for _, b := range doc.Blocks {
		if h, ok := b.(richdoc.Heading); ok {
			headings = append(headings, richdoc.PlainText(&richdoc.Document{Blocks: []richdoc.Block{h}}))
		}
	}
	if got := strings.Join(headings, "|"); !strings.Contains(got, "Slide 1") || !strings.Contains(got, "Slide 3") || len(headings) != 3 {
		t.Errorf("headings = %q, want three slides", got)
	}
}

// pkg is an unzipped package, for asserting what our own reader is too
// forgiving to notice.
type pkg map[string][]byte

func unzip(t *testing.T, b []byte) pkg {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := pkg{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = data
	}
	return out
}

// ⛔ The structural judge: every relationship's target is in the package,
// every part has a content type, every relationship id is unique in its part,
// and every slide id is unique and in the standard's range. Our reader skips
// a dangling relationship rather than failing, so it cannot be the judge here.
func TestWrittenPresentationIsWhole(t *testing.T) {
	files := unzip(t, writeDeck(t, deck(t)))

	var types struct {
		Default []struct {
			Extension   string `xml:",attr"`
			ContentType string `xml:",attr"`
		}
		Override []struct {
			PartName    string `xml:",attr"`
			ContentType string `xml:",attr"`
		}
	}
	if err := xml.Unmarshal(files["[Content_Types].xml"], &types); err != nil {
		t.Fatal(err)
	}
	byExt, byPart := map[string]string{}, map[string]string{}
	for _, d := range types.Default {
		byExt[d.Extension] = d.ContentType
	}
	for _, o := range types.Override {
		if !strings.HasPrefix(o.PartName, "/") {
			t.Errorf("override %q is not an absolute part name", o.PartName)
		}
		if _, there := files[strings.TrimPrefix(o.PartName, "/")]; !there {
			t.Errorf("override for %q, which is not in the package", o.PartName)
		}
		byPart[strings.TrimPrefix(o.PartName, "/")] = o.ContentType
	}
	for name := range files {
		if name == "[Content_Types].xml" {
			continue
		}
		if byPart[name] == "" && byExt[strings.TrimPrefix(path.Ext(name), ".")] == "" {
			t.Errorf("%s has no content type", name)
		}
	}
	for name, want := range map[string]string{
		"ppt/presentation.xml":  ctPresentation,
		"ppt/slides/slide1.xml": ctSlide,
		"ppt/media/image1.png":  "image/png",
		"ppt/media/image2.jpeg": "image/jpeg",
		"docProps/core.xml":     ctCore,
	} {
		got := byPart[name]
		if got == "" {
			got = byExt[strings.TrimPrefix(path.Ext(name), ".")]
		}
		if got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}

	rels := 0
	for name, data := range files {
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		rels++
		var list struct {
			Relationship []struct {
				ID         string `xml:"Id,attr"`
				Target     string `xml:",attr"`
				TargetMode string `xml:",attr"`
			}
		}
		if err := xml.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		// _rels/x.xml.rels is about x.xml, and its targets are relative to x's
		// directory.
		dir := path.Dir(path.Dir(name))
		seen := map[string]bool{}
		for _, r := range list.Relationship {
			if seen[r.ID] {
				t.Errorf("%s: id %s twice", name, r.ID)
			}
			seen[r.ID] = true
			if r.TargetMode == "External" {
				continue
			}
			target := path.Clean(path.Join(dir, r.Target))
			if _, there := files[target]; !there {
				t.Errorf("%s: %s points at %s, which is not in the package", name, r.ID, target)
			}
		}
	}
	// the package, presentation, master, layout, notes master, 3 slides, 2
	// notes slides: the slide without notes has no notes slide.
	if rels != 10 {
		t.Errorf("%d relationship parts, want 10", rels)
	}
	if _, there := files["ppt/notesSlides/notesSlide2.xml"]; there {
		t.Error("a slide with no notes has a notes slide")
	}

	var pres struct {
		SldIdLst struct {
			SldId []struct {
				// Every attribute, because r:id is an "id" too and the
				// one wanted is the one with no namespace.
				Attrs []xml.Attr `xml:",any,attr"`
			} `xml:"sldId"`
		} `xml:"sldIdLst"`
		SldSz struct {
			Cx int64 `xml:"cx,attr"`
			Cy int64 `xml:"cy,attr"`
		} `xml:"sldSz"`
	}
	if err := xml.Unmarshal(files["ppt/presentation.xml"], &pres); err != nil {
		t.Fatal(err)
	}
	ids := map[uint64]bool{}
	for _, s := range pres.SldIdLst.SldId {
		var id uint64
		for _, a := range s.Attrs {
			if a.Name.Space == "" && a.Name.Local == "id" {
				id, _ = strconv.ParseUint(a.Value, 10, 64)
			}
		}
		if id < 256 || id >= 1<<31 || ids[id] {
			t.Errorf("slide id %d is out of range or repeated", id)
		}
		ids[id] = true
	}
	if len(ids) != 3 || pres.SldSz.Cx != 12192000 || pres.SldSz.Cy != 6858000 {
		t.Errorf("%d slides at %d×%d, want 3 at 12192000×6858000", len(ids), pres.SldSz.Cx, pres.SldSz.Cy)
	}

	// Every part that is XML is well-formed — a writer that escaped one
	// string wrongly fails here and nowhere else.
	for name, data := range files {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("%s: %v", name, err)
				break
			}
		}
	}
}

// The notes are on the notes slide, a paragraph per line, escaped; the name is
// the slide's and the picture's alternative text.
func TestNotesAndNamesAreWritten(t *testing.T) {
	files := unzip(t, writeDeck(t, deck(t)))
	n1 := string(files["ppt/notesSlides/notesSlide1.xml"])
	if !strings.Contains(n1, "<a:t>first note</a:t></a:r></a:p><a:p><a:r><a:t>second line</a:t>") {
		t.Errorf("the notes are not a paragraph per line:\n%s", n1)
	}
	n3 := string(files["ppt/notesSlides/notesSlide3.xml"])
	if !strings.Contains(n3, "a &lt; b &amp; &#34;c&#34;") || !strings.Contains(n3, "<a:p/>") {
		t.Errorf("the notes are not escaped, or the blank line was lost:\n%s", n3)
	}
	s1 := string(files["ppt/slides/slide1.xml"])
	if !strings.Contains(s1, `<p:cSld name="Opening">`) || !strings.Contains(s1, `descr="Opening"`) {
		t.Errorf("the slide's name is not its name and alt text:\n%s", s1)
	}
	if !strings.Contains(string(files["ppt/slides/slide2.xml"]), `descr="Slide 2"`) {
		t.Error("an unnamed slide is not called by its position")
	}
	if !strings.Contains(string(files["ppt/slides/slide3.xml"]), `name="x &lt;y&gt;"`) {
		t.Error("the slide's name is not escaped")
	}
}

func TestCorePropertiesAreWritten(t *testing.T) {
	p := deck(t)
	core := string(unzip(t, writeDeck(t, p))["docProps/core.xml"])
	for _, want := range []string{
		"<dc:title>The Deck</dc:title>", "<dc:creator>Ada &amp; Bob</dc:creator>",
		"<dc:subject>A test</dc:subject>", "<cp:keywords>one, two</cp:keywords>",
	} {
		if !strings.Contains(core, want) {
			t.Errorf("%q is not in core.xml:\n%s", want, core)
		}
	}
	if strings.Contains(core, "<dcterms:") {
		t.Error("a date was written though none was given")
	}

	p.Created = time.Date(2026, 10, 18, 15, 4, 5, 0, time.FixedZone("CEST", 2*3600))
	core = string(unzip(t, writeDeck(t, p))["docProps/core.xml"])
	for _, want := range []string{
		`<dcterms:created xsi:type="dcterms:W3CDTF">2026-10-18T13:04:05Z</dcterms:created>`,
		`<dcterms:modified xsi:type="dcterms:W3CDTF">2026-10-18T13:04:05Z</dcterms:modified>`,
	} {
		if !strings.Contains(core, want) {
			t.Errorf("%q is not in core.xml:\n%s", want, core)
		}
	}

	// A property not given is not written, rather than written empty.
	p = deck(t)
	p.Author, p.Keywords = "", ""
	core = string(unzip(t, writeDeck(t, p))["docProps/core.xml"])
	if strings.Contains(core, "dc:creator>") || strings.Contains(core, "cp:keywords>") || !strings.Contains(core, "<dc:title>") {
		t.Errorf("only the given properties should be written:\n%s", core)
	}

	// With nothing to say there is no core part, and nothing points at one.
	p = deck(t)
	p.Title, p.Author, p.Subject, p.Keywords = "", "", "", ""
	files := unzip(t, writeDeck(t, p))
	if _, there := files["docProps/core.xml"]; there {
		t.Error("an empty core part was written")
	}
	if strings.Contains(string(files["_rels/.rels"]), "core-properties") {
		t.Error("the package points at a core part it does not have")
	}
}

// The same deck gives the same bytes: no clock, no map order.
func TestWritePresentationIsDeterministic(t *testing.T) {
	a, b := writeDeck(t, deck(t)), writeDeck(t, deck(t))
	if !bytes.Equal(a, b) {
		t.Error("two writes of one deck differ")
	}
}

func TestWritePresentationRefuses(t *testing.T) {
	defer func(s, i int, all int64, txt int) {
		maxWriteSlides, maxImageBytes, maxImagesBytes, maxTextBytes = s, i, all, txt
	}(maxWriteSlides, maxImageBytes, maxImagesBytes, maxTextBytes)

	cases := []struct {
		name  string
		edit  func(*Presentation)
		lower func()
		want  string
	}{
		{"narrow", func(p *Presentation) { p.Width = 914399 }, nil, "width"},
		{"tall", func(p *Presentation) { p.Height = 51206401 }, nil, "height"},
		{"empty", func(p *Presentation) { p.Slides = nil }, nil, "no slides"},
		{"many", func(p *Presentation) {}, func() { maxWriteSlides = 2 }, "3 slides"},
		{"type", func(p *Presentation) { p.Slides[0].ImageType = "gif" }, nil, `"gif"`},
		{"lying", func(p *Presentation) { p.Slides[1].ImageType = "png" }, nil, "not a png"},
		{"big", func(p *Presentation) {}, func() { maxImageBytes = 10 }, "slide 1: a picture"},
		{"all", func(p *Presentation) {}, func() { maxImagesBytes = int64(len(deck(t).Slides[0].Image)) + 1 }, "pictures come to"},
		{"notes", func(p *Presentation) { p.Slides[2].Notes = strings.Repeat("x", 26) }, func() { maxTextBytes = 25 }, "slide 3: its notes"},
		{"title", func(p *Presentation) { p.Title = strings.Repeat("x", 11) }, func() { maxTextBytes = 10 }, "document property"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			maxWriteSlides, maxImageBytes, maxImagesBytes, maxTextBytes = 1000, 64<<20, 192<<20, 1<<20
			if c.lower != nil {
				c.lower()
			}
			p := deck(t)
			c.edit(&p)
			_, err := WritePresentation(p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one saying %q", err, c.want)
			}
		})
	}
}
