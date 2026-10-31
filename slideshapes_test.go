// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// editable is a deck of editable slides: a background, every kind of shape,
// a shared picture shown on both slides, a link, text that needs escaping,
// and an embedded font.
func editable(t testing.TB) Presentation {
	p, _ := pictures(t)
	ring := [][]PathCmd{
		{{Op: 'M', P: [3]Point{{0, 0}}}, {Op: 'L', P: [3]Point{{100, 0}}}, {Op: 'L', P: [3]Point{{100, 100}}}, {Op: 'L', P: [3]Point{{0, 100}}}, {Op: 'Z'}},
		{{Op: 'M', P: [3]Point{{10, 10}}}, {Op: 'C', P: [3]Point{{50, 0}, {90, 50}, {90, 90}}}, {Op: 'L', P: [3]Point{{10, 90}}}, {Op: 'Z'}},
	}
	red := RGBA{R: 255, A: 255}
	slide := func(name string) PresentationSlide {
		return PresentationSlide{
			Name:       name,
			Notes:      "say it",
			Background: &Fill{Kind: FillSolid, Color: RGBA{R: 16, G: 32, B: 48, A: 255}},
			Shapes: []Shape{
				{X: 0, Y: 0, W: 914400, H: 457200, Fill: Fill{Kind: FillSolid, Color: RGBA{G: 128, B: 255, A: 128}}},
				{X: 10, Y: 10, W: 100, H: 100, Geometry: Geometry{Preset: "roundRect", Radius: 20}, Fill: Fill{Kind: FillGradient, Angle: -45, Stops: []GradientStop{{0, red}, {0.5, red}, {0.5, RGBA{B: 255}}, {1, RGBA{B: 255}}}}},
				{X: 10, Y: 10, W: 100, H: 100, Geometry: Geometry{Preset: "ellipse"}, Fill: Fill{Kind: FillGradient, Radial: true, Center: [2]float64{0.3, 0.4}, Stops: []GradientStop{{0, red}, {1, RGBA{A: 255}}}}},
				{X: 5, Y: 5, W: 100, H: 100, Geometry: Geometry{Paths: ring}, Fill: Fill{Kind: FillSolid, Color: red}},
				{X: 914400, Y: 914400, W: 3000000, H: 500000, Text: []TextRun{
					{Text: `Héllo <b>&"`, Font: "Lato", Size: 26.25, Color: RGBA{A: 255}, Spacing: 0.75},
					{Text: " link", Font: "Lato", Size: 26.25, Bold: true, Italic: true, Color: RGBA{B: 200, A: 255}, Link: "https://example.com/?a=1&b=2"},
				}},
				{X: 0, Y: 0, W: 914400, H: 514350, Image: 1, Crop: [4]float64{0.1, 0, 0.2, 0}, Name: "a picture"},
			},
		}
	}
	return Presentation{
		Width: 1280 * 9525, Height: 720 * 9525, Title: "Editable",
		Images: []Image{{Data: p, Type: "png"}},
		Fonts:  []Font{{Typeface: "Lato", Regular: []byte("font data"), BoldItal: []byte("more font data")}},
		Slides: []PresentationSlide{slide("One"), slide("Two")},
	}
}

func TestEditableSlidesReadBack(t *testing.T) {
	doc, err := ParsePresentation(writeDeck(t, editable(t)), opc.Limits{})
	if err != nil {
		t.Fatalf("what was written could not be read: %v", err)
	}
	text := richdoc.PlainText(doc)
	if !strings.Contains(text, `Héllo <b>&"`) || !strings.Contains(text, "link") {
		t.Errorf("the words of an editable slide do not read back: %q", text)
	}
}

// TestEditableSlidesAreWhole: every XML part is well-formed, every internal
// relationship lands in the package, the link is external, the picture is
// stored once for two slides, the font is related from the presentation and
// named in its embeddedFontLst.
func TestEditableSlidesAreWhole(t *testing.T) {
	files := unzip(t, writeDeck(t, editable(t)))
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
	media := 0
	for name := range files {
		if strings.HasPrefix(name, "ppt/media/") {
			media++
		}
	}
	if media != 1 {
		t.Errorf("%d media parts, want the one shared picture", media)
	}
	for _, rels := range []string{"ppt/slides/_rels/slide1.xml.rels", "ppt/slides/_rels/slide2.xml.rels", "ppt/_rels/presentation.xml.rels"} {
		var l2 struct {
			Relationship []struct {
				ID         string `xml:"Id,attr"`
				Target     string `xml:",attr"`
				Type       string `xml:",attr"`
				TargetMode string `xml:",attr"`
			}
		}
		if err := xml.Unmarshal(files[rels], &l2); err != nil {
			t.Fatal(err)
		}
		dir := path.Dir(path.Dir(rels))
		hyper := false
		for _, r := range l2.Relationship {
			if r.TargetMode == "External" {
				hyper = r.Type == relHyperlink && r.Target == "https://example.com/?a=1&b=2"
				continue
			}
			if _, there := files[path.Clean(path.Join(dir, r.Target))]; !there {
				t.Errorf("%s: %s points at %s, which is not in the package", rels, r.ID, r.Target)
			}
		}
		if strings.Contains(rels, "slides/") && !hyper {
			t.Errorf("%s has no external hyperlink relationship", rels)
		}
	}
	pres := string(files["ppt/presentation.xml"])
	for _, want := range []string{`embedTrueTypeFonts="1"`, `<p:embeddedFont><p:font typeface="Lato"/><p:regular r:id=`, `<p:boldItalic r:id=`} {
		if !strings.Contains(pres, want) {
			t.Errorf("presentation.xml lacks %s", want)
		}
	}
	if !strings.Contains(string(files["[Content_Types].xml"]), "application/x-font-ttf") {
		t.Error("the font parts are not typed application/x-font-ttf (§15.2.13)")
	}
	s1 := string(files["ppt/slides/slide1.xml"])
	for _, want := range []string{
		`<p:bg><p:bgPr><a:solidFill><a:srgbClr val="102030"/></a:solidFill>`,
		`<a:alpha val="50196"/>`,
		`<a:prstGeom prst="roundRect"><a:avLst><a:gd name="adj" fmla="val 20000"/>`,
		`<a:lin ang="18900000" scaled="0"/>`,
		`<a:path path="circle"><a:fillToRect l="30000" t="40000" r="70000" b="60000"/>`,
		`<a:cubicBezTo>`, `<a:close/>`,
		`<a:t>Héllo &lt;b&gt;&amp;&#34;</a:t>`,
		` spc="75"`, ` b="1" i="1"`, `<a:latin typeface="Lato"/>`,
		`<a:hlinkClick r:id=`,
		`<a:srcRect l="10000" t="0" r="20000" b="0"/>`,
		`descr="a picture"`,
	} {
		if !strings.Contains(s1, want) {
			t.Errorf("slide1.xml lacks %s", want)
		}
	}
}

// TestEditableDeckIsDeterministic: the same deck, the same bytes.
func TestEditableDeckIsDeterministic(t *testing.T) {
	if !bytes.Equal(writeDeck(t, editable(t)), writeDeck(t, editable(t))) {
		t.Error("two writes of one editable deck differ")
	}
}

func TestEditableSlidesRefuse(t *testing.T) {
	for name, mutate := range map[string]func(*Presentation){
		"an image index past the list":    func(p *Presentation) { p.Slides[0].Shapes[5].Image = 2 },
		"a negative size":                 func(p *Presentation) { p.Slides[0].Shapes[0].W = -1 },
		"a crop of a whole picture":       func(p *Presentation) { p.Slides[0].Shapes[5].Crop[0] = 1 },
		"an unknown preset":               func(p *Presentation) { p.Slides[0].Shapes[1].Geometry.Preset = "star5" },
		"an unknown path command":         func(p *Presentation) { p.Slides[0].Shapes[3].Geometry.Paths[0][1].Op = 'Q' },
		"a far point":                     func(p *Presentation) { p.Slides[0].Shapes[3].Geometry.Paths[0][1].P[0].X = 1 << 50 },
		"a one-stop gradient":             func(p *Presentation) { p.Slides[0].Shapes[2].Fill.Stops = p.Slides[0].Shapes[2].Fill.Stops[:1] },
		"stops out of order":              func(p *Presentation) { p.Slides[0].Shapes[1].Fill.Stops[0].Pos = 0.9 },
		"an unknown fill":                 func(p *Presentation) { p.Slides[0].Shapes[0].Fill.Kind = 9 },
		"a zero font size":                func(p *Presentation) { p.Slides[0].Shapes[4].Text[0].Size = 0 },
		"a javascript: link":              func(p *Presentation) { p.Slides[0].Shapes[4].Text[1].Link = "javascript:alert(1)" },
		"a file: link":                    func(p *Presentation) { p.Slides[0].Shapes[4].Text[1].Link = "file:///etc/passwd" },
		"a mailto with no address":        func(p *Presentation) { p.Slides[0].Shapes[4].Text[1].Link = "mailto:" },
		"a font with no name":             func(p *Presentation) { p.Fonts[0].Typeface = "" },
		"a picture that is not a PNG":     func(p *Presentation) { p.Images[0].Data = []byte("GIF89a") },
		"a gradient angle not a number":   func(p *Presentation) { p.Slides[0].Shapes[1].Fill.Angle = nan() },
		"a background of an unknown kind": func(p *Presentation) { p.Slides[0].Background.Kind = 7 },
		"a slide with nothing at all": func(p *Presentation) {
			p.Slides[0].Shapes, p.Slides[0].Background = nil, nil
		},
	} {
		p := editable(t)
		mutate(&p)
		if _, err := WritePresentation(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// A mailto link with an address is fine.
	p := editable(t)
	p.Slides[0].Shapes[4].Text[1].Link = "mailto:ada@example.com"
	if _, err := WritePresentation(p); err != nil {
		t.Errorf("mailto: %v", err)
	}
}

// TestEditableBounds lowers each bound to see it refuse.
func TestEditableBounds(t *testing.T) {
	long := strings.Repeat("x", 9000)
	for name, c := range map[string]struct {
		bound  *int
		to     int
		mutate func(*Presentation)
	}{
		"too many fonts":    {&maxFonts, 0, nil},
		"a font too large":  {&maxFontBytes, 3, nil},
		"too many shapes":   {&maxShapesPerSlide, 2, nil},
		"a long shape name": {&maxTextBytes, 8, func(p *Presentation) { p.Title, p.Slides[0].Notes, p.Slides[0].Shapes[0].Name = "", "", "a long name" }},
		"a long run": {&maxTextBytes, 8, func(p *Presentation) {
			p.Title, p.Slides[0].Notes, p.Slides[0].Shapes[4].Text[0].Text = "", "", "a long run"
		}},
		"a long link":        {nil, 0, func(p *Presentation) { p.Slides[0].Shapes[4].Text[1].Link = "https://e.com/" + long }},
		"a link that parses": {nil, 0, func(p *Presentation) { p.Slides[0].Shapes[4].Text[1].Link = "http://[::1" }},
		"a wild spacing":     {nil, 0, func(p *Presentation) { p.Slides[0].Shapes[4].Text[0].Spacing = 1e9 }},
	} {
		p := editable(t)
		old := 0
		if c.bound != nil {
			old, *c.bound = *c.bound, c.to
		}
		if c.mutate != nil {
			c.mutate(&p)
		}
		_, err := WritePresentation(p)
		if c.bound != nil {
			*c.bound = old
		}
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	old := maxImagesBytes
	maxImagesBytes = 10
	defer func() { maxImagesBytes = old }()
	if _, err := WritePresentation(editable(t)); err == nil {
		t.Error("pictures past the total: no error")
	}
}

// TestEditableSharesRelationships: a slide showing one picture twice and
// one link twice relates each once; an unnamed picture is numbered.
func TestEditableSharesRelationships(t *testing.T) {
	p := editable(t)
	s := &p.Slides[0]
	s.Shapes = append(s.Shapes, Shape{X: 1, Y: 1, W: 9, H: 9, Image: 1})
	s.Shapes[4].Text = append(s.Shapes[4].Text, s.Shapes[4].Text[1])
	files := unzip(t, writeDeck(t, p))
	rels := string(files["ppt/slides/_rels/slide1.xml.rels"])
	if strings.Count(rels, "relationships/image") != 1 || strings.Count(rels, "relationships/hyperlink") != 1 {
		t.Errorf("the picture and the link should be related once each:\n%s", rels)
	}
	if !strings.Contains(string(files["ppt/slides/slide1.xml"]), `name="Picture 8"`) {
		t.Error("an unnamed picture is not numbered")
	}
}

// group is a drawing of three shapes, one of them a group in its turn, and
// a word: what an SVG becomes on an editable slide.
func group() Shape {
	sq := [][]PathCmd{{{Op: 'M', P: [3]Point{{0, 0}}}, {Op: 'L', P: [3]Point{{50, 0}}}, {Op: 'L', P: [3]Point{{50, 50}}}, {Op: 'Z'}}}
	return Shape{X: 1000, Y: 2000, W: 300, H: 200, Name: "an SVG", Group: []Shape{
		{X: 1000, Y: 2000, W: 50, H: 50, Geometry: Geometry{Paths: sq}, Fill: Fill{Kind: FillSolid, Color: RGBA{R: 255, A: 255}}},
		{X: 1100, Y: 2000, W: 200, H: 100, Group: []Shape{
			{X: 1100, Y: 2000, W: 50, H: 50, Geometry: Geometry{Preset: "ellipse"}, Fill: Fill{Kind: FillSolid, Color: RGBA{B: 255, A: 255}}},
		}},
		{X: 1000, Y: 2100, W: 300000, H: 100000, Text: []TextRun{{Text: "grouped words", Size: 12, Color: RGBA{A: 255}}}},
	}}
}

// TestEditableGroups: a group is a p:grpSp whose child space is its own box,
// numbered before its members, nested groups inside it, and its text reads
// back.
func TestEditableGroups(t *testing.T) {
	p := editable(t)
	p.Slides[0].Shapes = append(p.Slides[0].Shapes, group())
	deck := writeDeck(t, p)
	s1 := string(unzip(t, deck)["ppt/slides/slide1.xml"])
	for _, want := range []string{
		`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="8" name="an SVG"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="1000" y="2000"/><a:ext cx="300" cy="200"/><a:chOff x="1000" y="2000"/><a:chExt cx="300" cy="200"/></a:xfrm></p:grpSpPr><p:sp><p:nvSpPr><p:cNvPr id="9" name="Shape 9"/>`,
		`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="10" name="Group 10"/>`,
		`<p:cNvPr id="11" name="Shape 11"/>`,
		`</p:sp></p:grpSp><p:sp><p:nvSpPr><p:cNvPr id="12" name="TextBox 12"/>`,
		`</p:sp></p:grpSp></p:spTree>`,
	} {
		if !strings.Contains(s1, want) {
			t.Errorf("slide1.xml lacks %s", want)
		}
	}
	doc, err := ParsePresentation(deck, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if text := richdoc.PlainText(doc); !strings.Contains(text, "grouped words") {
		t.Errorf("the words in a group do not read back: %q", text)
	}
}

// TestEditableGroupsRefuse: a group's members are checked like any shape,
// count towards the slide's limit, and nest MaxGroupDepth deep at most.
func TestEditableGroupsRefuse(t *testing.T) {
	p := editable(t)
	g := group()
	g.Group[1].Group[0].W = -1
	p.Slides[0].Shapes = append(p.Slides[0].Shapes, g)
	if _, err := WritePresentation(p); err == nil || !strings.Contains(err.Error(), "member 2: member 1:") {
		t.Errorf("a bad member: %v", err)
	}

	deep := Shape{W: 1, H: 1}
	for i := 0; i < MaxGroupDepth; i++ {
		deep = Shape{W: 1, H: 1, Group: []Shape{deep}}
	}
	p = editable(t)
	p.Slides[0].Shapes = append(p.Slides[0].Shapes, deep)
	if _, err := WritePresentation(p); err == nil || !strings.Contains(err.Error(), "nest") {
		t.Errorf("groups %d deep: %v", MaxGroupDepth+1, err)
	}
	p = editable(t)
	p.Slides[0].Shapes = append(p.Slides[0].Shapes, deep.Group[0].Group[0]) // MaxGroupDepth-1 groups
	if _, err := WritePresentation(p); err != nil {
		t.Errorf("groups %d deep: %v", MaxGroupDepth-1, err)
	}

	old := maxShapesPerSlide
	maxShapesPerSlide = len(editable(t).Slides[0].Shapes) + 2
	defer func() { maxShapesPerSlide = old }()
	p = editable(t)
	p.Slides[0].Shapes = append(p.Slides[0].Shapes, group()) // 1 + 3 + 1 shapes
	if _, err := WritePresentation(p); err == nil || !strings.Contains(err.Error(), "shapes is past") {
		t.Errorf("members past the limit: %v", err)
	}
	wide := Shape{W: 1, H: 1}
	for i := 0; i < 2*maxShapesPerSlide; i++ {
		wide.Group = append(wide.Group, Shape{W: 1, H: 1, Group: []Shape{{W: 1, H: 1}}})
	}
	if n := countShapes([]Shape{wide}, 0); n > 2*maxShapesPerSlide+2 {
		t.Errorf("countShapes went on to %d past the limit", n)
	}
}

func nan() float64 {
	var z float64
	return z / z
}

// FuzzEditableText: whatever a run's text or a shape's name holds, the
// slide stays well-formed XML and the text reads back without markup in it.
func FuzzEditableText(f *testing.F) {
	for _, s := range []string{"plain", "<a:t>", `"&'<>`, "\x00\x01\x1f", "￾￿", "]]>", "\xff\xfe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p := editable(t)
		p.Slides[0].Shapes[4].Text[0].Text = s
		p.Slides[0].Shapes[5].Name = s
		p.Fonts[0].Typeface = "F" + s
		b, err := WritePresentation(p)
		if err != nil {
			return // a refusal is an answer
		}
		files := unzip(t, b)
		for _, name := range []string{"ppt/slides/slide1.xml", "ppt/presentation.xml"} {
			dec := xml.NewDecoder(bytes.NewReader(files[name]))
			for {
				_, err := dec.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("%s is not well-formed for %q: %v", name, s, err)
				}
			}
		}
	})
}

// TestLibreOfficeOpensTheEditableDeck renders the deck to PDF and reads the
// words back off it: the text boxes are text a renderer draws.
func TestLibreOfficeOpensTheEditableDeck(t *testing.T) {
	soffice := findSoffice()
	if soffice == "" {
		t.Skip("LibreOffice is not installed")
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext is not installed")
	}
	dir := t.TempDir()
	p := editable(t)
	p.Fonts = nil // the fake font data is not a font
	p.Slides[0].Shapes = append(p.Slides[0].Shapes, group())
	in := filepath.Join(dir, "deck.pptx")
	if err := os.WriteFile(in, writeDeck(t, p), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(soffice, "--headless", "--convert-to", "pdf", "--outdir", dir, in)
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("soffice: %v\n%s", err, out)
	}
	txt, err := exec.Command("pdftotext", filepath.Join(dir, "deck.pdf"), "-").Output()
	if err != nil {
		t.Fatalf("LibreOffice wrote no PDF: %v", err)
	}
	if !strings.Contains(string(txt), "Héllo <b>&\"") {
		t.Errorf("the PDF LibreOffice made does not hold the slide's text: %q", txt)
	}
	if !strings.Contains(string(txt), "grouped words") {
		t.Errorf("LibreOffice did not draw the text of a group: %q", txt)
	}
}
