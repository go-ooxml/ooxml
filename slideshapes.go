// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// A Shape is one element of an editable slide: a DrawingML shape (p:sp,
// ECMA-376 Part 1 §19.3.1.43) — a geometry with a fill, and optionally text —
// or a picture (p:pic, §19.3.1.37). Shapes are drawn in order, the first at
// the back.
//
// Positions and sizes are in EMU, 914400 to the inch (a CSS pixel is 9525).
type Shape struct {
	X, Y, W, H int64

	// Geometry is the outline; the zero value is a rectangle.
	Geometry Geometry

	// Fill paints the inside of the geometry. The zero value is no fill.
	Fill Fill

	// Text makes the shape a text box: one paragraph of runs, set from the
	// box's top-left corner with no inset and no wrapping, so that it stays
	// on the line it was laid out on. An empty Text is a plain shape.
	Text []TextRun

	// Image, when not zero, makes the shape a picture of Presentation.Images
	// [Image-1], stretched over the box. Geometry, Fill and Text are then
	// ignored. Crop removes the given fractions of the picture from its
	// left, top, right and bottom before it is stretched (a:srcRect).
	Image int
	Crop  [4]float64

	// Name names the shape (p:cNvPr name); for a picture it is also the
	// alternative text. Empty gives a numbered name.
	Name string

	// Group, when not empty, makes the shape a group (p:grpSp, §19.3.1.22)
	// of these shapes, which a viewer selects, moves and scales as one: an
	// SVG drawing, a formula. The children are placed in slide coordinates
	// like any shape, and X, Y, W, H is the group's box (its a:xfrm, whose
	// child offset and extents equal it, so the children are where they
	// say). Geometry, Fill, Text and Image are then ignored. A group may hold
	// groups, MaxGroupDepth deep; every shape in it counts towards the
	// slide's limit.
	Group []Shape
}

// MaxGroupDepth bounds how deep groups nest: a reader walks the tree
// recursively.
const MaxGroupDepth = 32

// Geometry is a shape's outline (§20.1.9). Preset is one of the preset
// shapes: "" or "rect", "roundRect" (corner radius Radius EMU), "ellipse".
// Paths, when not empty, is a custom geometry instead (a:custGeom): each path
// a closed outline of commands in EMU, relative to the shape's top-left
// corner. Several paths make one outline whose overlaps are holes, which is
// how a ring is drawn.
type Geometry struct {
	Preset string
	Radius int64
	Paths  [][]PathCmd
}

// PathCmd is one command of a custom path: 'M' move to P[0], 'L' line to
// P[0], 'C' cubic Bézier through P[0] and P[1] to P[2], 'Z' close.
type PathCmd struct {
	Op byte
	P  [3]Point
}

// Point is a point in EMU.
type Point struct{ X, Y int64 }

// RGBA is a colour with an alpha: 255 is opaque.
type RGBA struct{ R, G, B, A uint8 }

// FillKind says what a Fill is.
type FillKind uint8

// The fills.
const (
	FillNone     FillKind = iota // nothing
	FillSolid                    // Color
	FillGradient                 // Stops along Angle, or around the centre when Radial
)

// Fill is how a shape's inside, or a slide's background, is painted
// (§20.1.8). A gradient's Stops are at positions from 0 to 1, increasing;
// a linear one runs at Angle degrees clockwise from the x axis across the
// shape's box (a:lin, unscaled), a radial one (a:path path="circle") from its
// centre, at Center as fractions of the box, outwards to its corners.
type Fill struct {
	Kind   FillKind
	Color  RGBA
	Stops  []GradientStop
	Angle  float64
	Radial bool
	Center [2]float64
}

// GradientStop is one colour of a gradient.
type GradientStop struct {
	Pos   float64
	Color RGBA
}

// TextRun is a stretch of text in one style. Font names the typeface (an
// embedded one, see Presentation.Fonts, or one the viewer has); Size is in
// points. Link, when set, is an http, https or mailto URL the run opens.
type TextRun struct {
	Text         string
	Font         string
	Size         float64
	Bold, Italic bool
	Color        RGBA
	Link         string
	// Spacing is added after each character, in points (a:rPr spc).
	Spacing float64
}

// Image is a picture shapes refer to by its position in
// Presentation.Images, plus one: each is stored once whatever number of
// slides show it.
type Image struct {
	Data []byte
	Type string // "png" or "jpeg"
}

// Font is a typeface embedded in the deck (p:embeddedFontLst, §19.2.1.10):
// TrueType or OpenType data for its regular, bold, italic and bold italic
// faces, any of which may be nil. Typeface is the name text runs give.
type Font struct {
	Typeface                        string
	Regular, Bold, Italic, BoldItal []byte
}

// Bounds on editable slides, for the same reason as the others: a writer
// whose output its own reader refuses writes dead files.
var (
	maxShapesPerSlide = 20000
	maxFontBytes      = 32 << 20
	maxFonts          = 64
	maxCoordinate     = int64(1) << 40
)

// checkShapes refuses the slide content WritePresentation would otherwise
// write as a file a viewer refuses.
func checkShapes(p Presentation) error {
	if len(p.Fonts) > maxFonts {
		return fmt.Errorf("ooxml: %d embedded fonts is past the %d allowed", len(p.Fonts), maxFonts)
	}
	for _, f := range p.Fonts {
		if f.Typeface == "" || len(f.Typeface) > 256 {
			return fmt.Errorf("ooxml: an embedded font needs a typeface name of at most 256 bytes")
		}
		for _, d := range [][]byte{f.Regular, f.Bold, f.Italic, f.BoldItal} {
			if len(d) > maxFontBytes {
				return fmt.Errorf("ooxml: an embedded face of %d bytes is past the %d allowed", len(d), maxFontBytes)
			}
		}
	}
	var total int64
	for i, img := range p.Images {
		if err := checkImage(PresentationSlide{Image: img.Data, ImageType: img.Type}); err != nil {
			return fmt.Errorf("ooxml: image %d: %w", i+1, err)
		}
		total += int64(len(img.Data))
	}
	for _, s := range p.Slides {
		total += int64(len(s.Image))
	}
	if total > maxImagesBytes {
		return fmt.Errorf("ooxml: the pictures come to more than the %d bytes allowed", maxImagesBytes)
	}
	for i, s := range p.Slides {
		if n := countShapes(s.Shapes, 0); n > maxShapesPerSlide {
			return fmt.Errorf("ooxml: slide %d: %d shapes is past the %d allowed", i+1, n, maxShapesPerSlide)
		}
		if s.Background != nil {
			if err := checkFill(*s.Background); err != nil {
				return fmt.Errorf("ooxml: slide %d background: %w", i+1, err)
			}
		}
		for j, sh := range s.Shapes {
			if err := checkShape(sh, len(p.Images), 0); err != nil {
				return fmt.Errorf("ooxml: slide %d shape %d: %w", i+1, j+1, err)
			}
		}
	}
	return nil
}

// countShapes counts the shapes of a tree, groups and their members, and
// stops counting past the limit (or past MaxGroupDepth, which checkShape
// refuses): it bounds its own work.
func countShapes(shapes []Shape, depth int) int {
	n := len(shapes)
	if depth >= MaxGroupDepth {
		return n
	}
	for _, sh := range shapes {
		if n > maxShapesPerSlide {
			return n
		}
		n += countShapes(sh.Group, depth+1)
	}
	return n
}

func coordOK(v int64) bool { return v > -maxCoordinate && v < maxCoordinate }

func checkShape(sh Shape, images, depth int) error {
	if !coordOK(sh.X) || !coordOK(sh.Y) || sh.W < 0 || sh.H < 0 || sh.W >= maxCoordinate || sh.H >= maxCoordinate {
		return fmt.Errorf("its box %d,%d %d×%d is out of range", sh.X, sh.Y, sh.W, sh.H)
	}
	if len(sh.Name) > maxTextBytes {
		return fmt.Errorf("its name is past the %d bytes allowed", maxTextBytes)
	}
	if len(sh.Group) > 0 {
		if depth+1 >= MaxGroupDepth {
			return fmt.Errorf("groups nest past the %d levels allowed", MaxGroupDepth)
		}
		for k, c := range sh.Group {
			if err := checkShape(c, images, depth+1); err != nil {
				return fmt.Errorf("member %d: %w", k+1, err)
			}
		}
		return nil
	}
	if sh.Image != 0 {
		if sh.Image < 0 || sh.Image > images {
			return fmt.Errorf("it shows image %d of %d", sh.Image, images)
		}
		for _, c := range sh.Crop {
			if !(c >= 0 && c < 1) {
				return fmt.Errorf("a crop of %v is not a fraction", c)
			}
		}
		return nil
	}
	switch sh.Geometry.Preset {
	case "", "rect", "roundRect", "ellipse":
	default:
		return fmt.Errorf("preset geometry %q is not rect, roundRect or ellipse", sh.Geometry.Preset)
	}
	for _, path := range sh.Geometry.Paths {
		for _, c := range path {
			switch c.Op {
			case 'M', 'L', 'C', 'Z':
			default:
				return fmt.Errorf("path command %q is not M, L, C or Z", c.Op)
			}
			for _, pt := range c.P {
				if !coordOK(pt.X) || !coordOK(pt.Y) {
					return fmt.Errorf("a path point is out of range")
				}
			}
		}
	}
	if err := checkFill(sh.Fill); err != nil {
		return err
	}
	for _, r := range sh.Text {
		if len(r.Text) > maxTextBytes || len(r.Font) > 256 {
			return fmt.Errorf("a text run or its font name is too long")
		}
		if !(r.Size >= 1 && r.Size <= 4000) {
			return fmt.Errorf("a font size of %v pt is outside 1 to 4000", r.Size)
		}
		if !(r.Spacing > -4000 && r.Spacing < 4000) {
			return fmt.Errorf("a letter spacing of %v pt is outside ±4000", r.Spacing)
		}
		if r.Link != "" && !linkOK(r.Link) {
			return fmt.Errorf("link %q is not an http, https or mailto URL", r.Link)
		}
	}
	return nil
}

func checkFill(f Fill) error {
	switch f.Kind {
	case FillNone, FillSolid:
		return nil
	case FillGradient:
		if len(f.Stops) < 2 || len(f.Stops) > 256 {
			return fmt.Errorf("a gradient of %d stops; 2 to 256 are allowed", len(f.Stops))
		}
		for i, s := range f.Stops {
			if !(s.Pos >= 0 && s.Pos <= 1) || (i > 0 && s.Pos < f.Stops[i-1].Pos) {
				return fmt.Errorf("gradient stop %d at %v is not in order within 0 to 1", i, s.Pos)
			}
		}
		if math.IsNaN(f.Angle) || math.IsInf(f.Angle, 0) {
			return fmt.Errorf("a gradient angle of %v", f.Angle)
		}
		return nil
	}
	return fmt.Errorf("fill kind %d is unknown", f.Kind)
}

// linkOK accepts the URLs a run may open: a link that runs a script or
// reaches into the file system has no business in a slide.
func linkOK(s string) bool {
	if len(s) > 8192 {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != ""
	}
	return false
}

// slideParts is what one slide's shapes need from its relationship part:
// the pictures and links it uses, numbered after the layout (rId1).
type slideParts struct {
	images map[int]string    // image index -> rId
	links  map[string]string // URL -> rId
	order  []slideRel
	next   int
}

type slideRel struct {
	id, typ, target string
	external        bool
}

func newSlideParts(first int) *slideParts {
	return &slideParts{images: map[int]string{}, links: map[string]string{}, next: first}
}

func (sp *slideParts) image(i int, target string) string {
	if id, ok := sp.images[i]; ok {
		return id
	}
	id := "rId" + strconv.Itoa(sp.next)
	sp.next++
	sp.images[i] = id
	sp.order = append(sp.order, slideRel{id: id, typ: relImage, target: target})
	return id
}

func (sp *slideParts) link(u string) string {
	if id, ok := sp.links[u]; ok {
		return id
	}
	id := "rId" + strconv.Itoa(sp.next)
	sp.next++
	sp.links[u] = id
	sp.order = append(sp.order, slideRel{id: id, typ: relHyperlink, target: u, external: true})
	return id
}

const relHyperlink = relDoc + "hyperlink"

// writeShapes writes a slide's shapes into its spTree, numbering them from
// first (1 is the tree itself). The background, p:bg, comes before the tree
// and is writeBackground's.
func writeShapes(b *bytes.Buffer, shapes []Shape, first int, sp *slideParts, imageTarget func(int) string) {
	next := first
	writeShapeList(b, shapes, &next, sp, imageTarget)
}

// writeShapeList writes shapes in order, numbering them from *next on, the
// members of a group after the group itself.
func writeShapeList(b *bytes.Buffer, shapes []Shape, next *int, sp *slideParts, imageTarget func(int) string) {
	for _, sh := range shapes {
		id := strconv.Itoa(*next)
		*next++
		name := sh.Name
		if len(sh.Group) > 0 {
			if name == "" {
				name = "Group " + id
			}
			b.WriteString(`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="` + id + `" name="`)
			esc(b, name)
			b.WriteString(`"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr>`)
			fmt.Fprintf(b, `<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/><a:chOff x="%d" y="%d"/><a:chExt cx="%d" cy="%d"/></a:xfrm>`,
				sh.X, sh.Y, sh.W, sh.H, sh.X, sh.Y, sh.W, sh.H)
			b.WriteString(`</p:grpSpPr>`)
			writeShapeList(b, sh.Group, next, sp, imageTarget)
			b.WriteString(`</p:grpSp>`)
			continue
		}
		if sh.Image != 0 {
			if name == "" {
				name = "Picture " + id
			}
			rid := sp.image(sh.Image, imageTarget(sh.Image))
			b.WriteString(`<p:pic><p:nvPicPr><p:cNvPr id="` + id + `" name="`)
			esc(b, name)
			b.WriteString(`" descr="`)
			esc(b, name)
			b.WriteString(`"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`)
			b.WriteString(`<p:blipFill><a:blip r:embed="` + rid + `"/>`)
			if sh.Crop != [4]float64{} {
				fmt.Fprintf(b, `<a:srcRect l="%d" t="%d" r="%d" b="%d"/>`, pct(sh.Crop[0]), pct(sh.Crop[1]), pct(sh.Crop[2]), pct(sh.Crop[3]))
			}
			b.WriteString(`<a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>`)
			xfrm(b, sh)
			b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`)
			continue
		}
		if name == "" {
			if len(sh.Text) > 0 {
				name = "TextBox " + id
			} else {
				name = "Shape " + id
			}
		}
		b.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="` + id + `" name="`)
		esc(b, name)
		b.WriteString(`"/><p:cNvSpPr`)
		if len(sh.Text) > 0 {
			b.WriteString(` txBox="1"`)
		}
		b.WriteString(`/><p:nvPr/></p:nvSpPr><p:spPr>`)
		xfrm(b, sh)
		geometry(b, sh)
		writeFill(b, sh.Fill)
		b.WriteString(`<a:ln><a:noFill/></a:ln></p:spPr>`)
		if len(sh.Text) > 0 {
			// No inset, no wrap, anchored at the top: the text sits where the
			// layout put it, one line per box (§21.1.2.1.1).
			b.WriteString(`<p:txBody><a:bodyPr wrap="none" lIns="0" tIns="0" rIns="0" bIns="0" anchor="t"><a:noAutofit/></a:bodyPr><a:lstStyle/><a:p>`)
			for _, r := range sh.Text {
				writeRun(b, r, sp)
			}
			b.WriteString(`</a:p></p:txBody>`)
		}
		b.WriteString(`</p:sp>`)
	}
}

// pct is a fraction as ST_Percentage in thousandths of a percent.
func pct(f float64) int64 { return int64(math.Round(f * 100000)) }

func xfrm(b *bytes.Buffer, sh Shape) {
	fmt.Fprintf(b, `<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, sh.X, sh.Y, sh.W, sh.H)
}

func geometry(b *bytes.Buffer, sh Shape) {
	g := sh.Geometry
	if len(g.Paths) > 0 {
		b.WriteString(`<a:custGeom><a:avLst/><a:gdLst/><a:ahLst/><a:cxnLst/><a:rect l="0" t="0" r="r" b="b"/><a:pathLst>`)
		// One a:path: its subpaths' overlaps are holes, which is what makes a
		// border ring or a cut-out shadow.
		fmt.Fprintf(b, `<a:path w="%d" h="%d">`, max(sh.W, 1), max(sh.H, 1))
		for _, path := range g.Paths {
			for _, c := range path {
				switch c.Op {
				case 'M':
					fmt.Fprintf(b, `<a:moveTo><a:pt x="%d" y="%d"/></a:moveTo>`, c.P[0].X, c.P[0].Y)
				case 'L':
					fmt.Fprintf(b, `<a:lnTo><a:pt x="%d" y="%d"/></a:lnTo>`, c.P[0].X, c.P[0].Y)
				case 'C':
					fmt.Fprintf(b, `<a:cubicBezTo><a:pt x="%d" y="%d"/><a:pt x="%d" y="%d"/><a:pt x="%d" y="%d"/></a:cubicBezTo>`,
						c.P[0].X, c.P[0].Y, c.P[1].X, c.P[1].Y, c.P[2].X, c.P[2].Y)
				case 'Z':
					b.WriteString(`<a:close/>`)
				}
			}
		}
		b.WriteString(`</a:path></a:pathLst></a:custGeom>`)
		return
	}
	switch g.Preset {
	case "roundRect":
		// adj is the radius as a fraction of the shorter side, in
		// 1/100000, at most half of it (§20.1.10.56, presetShapeDefinitions).
		short := min(sh.W, sh.H)
		adj := int64(0)
		if short > 0 {
			adj = min(g.Radius*100000/short, 50000)
		}
		fmt.Fprintf(b, `<a:prstGeom prst="roundRect"><a:avLst><a:gd name="adj" fmla="val %d"/></a:avLst></a:prstGeom>`, adj)
	case "ellipse":
		b.WriteString(`<a:prstGeom prst="ellipse"><a:avLst/></a:prstGeom>`)
	default:
		b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)
	}
}

// srgb writes a:srgbClr, with an a:alpha when not opaque.
func srgb(b *bytes.Buffer, c RGBA) {
	fmt.Fprintf(b, `<a:srgbClr val="%02X%02X%02X"`, c.R, c.G, c.B)
	if c.A == 255 {
		b.WriteString(`/>`)
		return
	}
	fmt.Fprintf(b, `><a:alpha val="%d"/></a:srgbClr>`, int64(c.A)*100000/255)
}

func writeFill(b *bytes.Buffer, f Fill) {
	switch f.Kind {
	case FillSolid:
		b.WriteString(`<a:solidFill>`)
		srgb(b, f.Color)
		b.WriteString(`</a:solidFill>`)
	case FillGradient:
		b.WriteString(`<a:gradFill rotWithShape="1"><a:gsLst>`)
		for _, s := range f.Stops {
			fmt.Fprintf(b, `<a:gs pos="%d">`, pct(s.Pos))
			srgb(b, s.Color)
			b.WriteString(`</a:gs>`)
		}
		b.WriteString(`</a:gsLst>`)
		if f.Radial {
			cx, cy := pct(f.Center[0]), pct(f.Center[1])
			fmt.Fprintf(b, `<a:path path="circle"><a:fillToRect l="%d" t="%d" r="%d" b="%d"/></a:path>`, cx, cy, 100000-cx, 100000-cy)
		} else {
			// ST_PositiveFixedAngle: 60000ths of a degree, 0 to 21600000.
			a := math.Mod(f.Angle, 360)
			if a < 0 {
				a += 360
			}
			fmt.Fprintf(b, `<a:lin ang="%d" scaled="0"/>`, int64(math.Round(a*60000))%21600000)
		}
		b.WriteString(`</a:gradFill>`)
	default:
		b.WriteString(`<a:noFill/>`)
	}
}

func writeRun(b *bytes.Buffer, r TextRun, sp *slideParts) {
	fmt.Fprintf(b, `<a:r><a:rPr lang="en-US" sz="%d"`, int64(math.Round(r.Size*100)))
	if r.Bold {
		b.WriteString(` b="1"`)
	}
	if r.Italic {
		b.WriteString(` i="1"`)
	}
	if spc := int64(math.Round(r.Spacing * 100)); spc != 0 {
		// ST_TextPoint: hundredths of a point.
		fmt.Fprintf(b, ` spc="%d"`, spc)
	}
	b.WriteString(` dirty="0"><a:solidFill>`)
	srgb(b, r.Color)
	b.WriteString(`</a:solidFill>`)
	if r.Font != "" {
		for _, el := range []string{"latin", "ea", "cs"} {
			b.WriteString(`<a:` + el + ` typeface="`)
			esc(b, r.Font)
			b.WriteString(`"/>`)
		}
	}
	if r.Link != "" {
		b.WriteString(`<a:hlinkClick r:id="` + sp.link(r.Link) + `"/>`)
	}
	b.WriteString(`</a:rPr><a:t>`)
	esc(b, r.Text)
	b.WriteString(`</a:t></a:r>`)
}

// writeBackground writes p:bg (§19.3.1.1), which comes first in p:cSld.
func writeBackground(b *bytes.Buffer, f *Fill) {
	if f == nil || f.Kind == FillNone {
		return
	}
	b.WriteString(`<p:bg><p:bgPr>`)
	writeFill(b, *f)
	b.WriteString(`<a:effectLst/></p:bgPr></p:bg>`)
}

// imageName is the part name, under ppt/, of Presentation.Images[i-1].
func imageName(i int, img Image) string {
	return "media/shared" + strconv.Itoa(i) + "." + img.Type
}
