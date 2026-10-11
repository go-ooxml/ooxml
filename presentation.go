// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// relSlide names one slide of a presentation.
const relSlide = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide"

// maxSlides bounds a presentation the way maxDepth bounds a document. A slide
// costs a heading and a walk, so the ceiling is generous; it is here so that a
// file CLAIMING four million slides is refused rather than walked.
const maxSlides = 16384

// ParsePresentation reads a PresentationML package (.pptx) into a document:
// each slide as a heading of its position, followed by the text found in its
// shapes.
//
// ⛔ The slides come out in the order the PRESENTATION gives, which is
// p:sldIdLst — not the order of the relationships, and above all not the order
// of the filenames. slide1.xml, slide2.xml … happen to be in order in a file
// straight out of a converter, and stop being in order the moment somebody
// moves a slide: the names stay where they were and the list changes. A reader
// that sorted by filename would silently reorder the deck.
func ParsePresentation(src []byte, lim opc.Limits) (*richdoc.Document, error) {
	p, err := opc.Open(src, lim)
	if err != nil {
		return nil, err
	}
	main, err := p.Main()
	if err != nil {
		return nil, err
	}
	presentation, err := p.Part(main)
	if err != nil {
		return nil, err
	}
	order, err := slideOrder(presentation)
	if err != nil {
		return nil, err
	}
	rels, err := p.Rels(main)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]opc.Rel, len(rels))
	for _, r := range rels {
		byID[r.ID] = r
	}
	var blocks []richdoc.Block
	n := 0
	for _, id := range order {
		r, ok := byID[id]
		if !ok || !opc.SameRelType(r.Type, relSlide) || r.External {
			// ⛔ A sldId naming a relationship that is not a slide is skipped
			// rather than fatal. A deck whose list has grown a stale entry
			// still has its other slides, and refusing the file would lose
			// them all.
			continue
		}
		name, err := p.Resolve(main, r)
		if err != nil {
			continue
		}
		data, err := p.Part(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := checkDepth(data); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		n++
		s := &slideReader{}
		inner, err := s.slide(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		// ⛔ The heading is the slide's POSITION, not a title taken from a
		// shape. A .pptx carries no name for a slide, and the placeholder that
		// would say which shape is the title is absent from a converted deck —
		// the same thing go-odf found for .odp, where presentation:class is
		// stripped. "Slide 3" is dull and always right.
		blocks = append(blocks, richdoc.Heading{
			Level:   1,
			Inlines: []richdoc.Inline{richdoc.Text{Value: "Slide " + strconv.Itoa(n)}},
		})
		blocks = append(blocks, inner...)
	}
	if n == 0 {
		return nil, fmt.Errorf("ooxml: the presentation names no slides")
	}
	return &richdoc.Document{Blocks: blocks}, nil
}

// slideOrder reads p:sldIdLst for the relationship ids of the slides, in
// order.
func slideOrder(data []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	inList := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "sldIdLst":
				inList = true
			case "sldId":
				if inList {
					if len(out) >= maxSlides {
						return nil, fmt.Errorf("ooxml: the presentation lists more than %d slides", maxSlides)
					}
					if id := attrNS(e, "id"); id != "" {
						out = append(out, id)
					}
				}
			}
		case xml.EndElement:
			if e.Name.Local == "sldIdLst" {
				return out, nil
			}
		}
	}
}

// slideReader walks one slide.
type slideReader struct{}

func (s *slideReader) slide(data []byte) ([]richdoc.Block, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []richdoc.Block
	// The list being accumulated, if any.
	var items []richdoc.ListItem
	ordered := false
	flush := func() {
		if len(items) == 0 {
			return
		}
		out = append(out, richdoc.List{Ordered: ordered, Items: items, Tight: true})
		items, ordered = nil, false
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			flush()
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "tbl":
			flush()
			t, err := s.table(dec, se)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		case "p":
			para, err := s.paragraph(dec, se)
			if err != nil {
				return nil, err
			}
			if len(para.inlines) == 0 {
				// An empty a:p is how a deck makes space, exactly as an empty
				// w:p is in Word.
				continue
			}
			if para.bullet {
				if len(items) > 0 && para.ordered != ordered {
					flush()
				}
				ordered = para.ordered
				items = append(items, richdoc.ListItem{
					Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: para.inlines}},
				})
				continue
			}
			flush()
			out = append(out, richdoc.Paragraph{Inlines: para.inlines})
		}
	}
}

type slidePara struct {
	inlines []richdoc.Inline
	bullet  bool
	ordered bool
}

func (s *slideReader) paragraph(dec *xml.Decoder, se xml.StartElement) (slidePara, error) {
	var out slidePara
	for {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return out, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "pPr":
				b, o, err := s.paragraphProps(dec, e)
				if err != nil {
					return out, err
				}
				out.bullet, out.ordered = b, o
			case "r":
				ins, err := s.run(dec, e)
				if err != nil {
					return out, err
				}
				out.inlines = append(out.inlines, ins...)
			case "br":
				out.inlines = append(out.inlines, richdoc.LineBreak{})
				if err := dec.Skip(); err != nil {
					return out, err
				}
			case "fld":
				// A field on a slide — the page number, the date. Its a:t
				// holds the value a viewer SHOWED, unlike Word's instrText
				// which holds the instructions, so it is read.
				ins, err := s.run(dec, e)
				if err != nil {
					return out, err
				}
				out.inlines = append(out.inlines, ins...)
			default:
				if err := dec.Skip(); err != nil {
					return out, err
				}
			}
		}
	}
}

// paragraphProps reads an a:pPr for whether the paragraph is a list item.
//
// ⛔ a:buNone says explicitly that it is NOT, and it has to be honoured:
// a converted deck puts marL and indent on every paragraph of a text box, so
// an indent alone is not a bullet. buChar is a bulleted item, buAutoNum a
// numbered one.
func (s *slideReader) paragraphProps(dec *xml.Decoder, se xml.StartElement) (bullet, ordered bool, err error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return false, false, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return bullet, ordered, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "buChar":
				bullet = true
			case "buAutoNum":
				bullet, ordered = true, true
			case "buNone":
				bullet, ordered = false, false
			}
			if err := dec.Skip(); err != nil {
				return false, false, err
			}
		}
	}
}

func (s *slideReader) run(dec *xml.Decoder, se xml.StartElement) ([]richdoc.Inline, error) {
	var style runStyle
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				if text.Len() == 0 {
					return nil, nil
				}
				return []richdoc.Inline{wrap(richdoc.Text{Value: text.String()}, style)}, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "rPr":
				style = drawingRunStyle(e)
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			case "t":
				str, err := flatText(dec, e.Name)
				if err != nil {
					return nil, err
				}
				text.WriteString(str)
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

// drawingRunStyle reads a:rPr, whose properties are ATTRIBUTES.
//
// ⛔⛔ This is the trap that makes a shared implementation wrong. In
// WordprocessingML a run's formatting is CHILD ELEMENTS — <w:b/>, <w:i/> — and
// in DrawingML it is attributes on the element itself: b="1", i="1",
// strike="sngStrike", u="sng". A reader that reused [runProps] here would find
// no formatting at all in a .pptx, because every child of a:rPr is a colour or
// a font and none of them is a toggle. Measured: LibreOffice writes
// a:rPr b="0" strike="noStrike" u="none" on every run of a converted deck.
func drawingRunStyle(se xml.StartElement) runStyle {
	on := func(v string) bool {
		switch strings.ToLower(v) {
		case "1", "true", "on":
			return true
		}
		return false
	}
	return runStyle{
		bold:   on(attr(se, "b")),
		italic: on(attr(se, "i")),
		// ⛔ These two are named values, not booleans: "none" and "noStrike"
		// are off and anything else is a kind of underline or strike. An
		// empty attribute is absent and must not read as on.
		underline: attr(se, "u") != "" && attr(se, "u") != "none",
		strike:    attr(se, "strike") != "" && attr(se, "strike") != "noStrike",
	}
}

func (s *slideReader) table(dec *xml.Decoder, se xml.StartElement) (richdoc.Block, error) {
	var rows [][]richdoc.Cell
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return richdoc.Table{Rows: rows}, nil
			}
		case xml.StartElement:
			if e.Name.Local == "tr" {
				cells, err := s.row(dec, e)
				if err != nil {
					return nil, err
				}
				rows = append(rows, cells)
				continue
			}
			if err := dec.Skip(); err != nil {
				return nil, err
			}
		}
	}
}

func (s *slideReader) row(dec *xml.Decoder, se xml.StartElement) ([]richdoc.Cell, error) {
	var cells []richdoc.Cell
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return cells, nil
			}
		case xml.StartElement:
			if e.Name.Local == "tc" {
				var inlines []richdoc.Inline
				depth := 0
			cell:
				for {
					tok, err := dec.Token()
					if err != nil {
						return nil, err
					}
					switch ce := tok.(type) {
					case xml.EndElement:
						if depth == 0 && ce.Name == e.Name {
							break cell
						}
						depth--
					case xml.StartElement:
						if ce.Name.Local == "r" || ce.Name.Local == "fld" {
							ins, err := s.run(dec, ce)
							if err != nil {
								return nil, err
							}
							inlines = append(inlines, ins...)
							continue
						}
						depth++
					}
				}
				cells = append(cells, richdoc.Cell{Inlines: inlines})
				continue
			}
			if err := dec.Skip(); err != nil {
				return nil, err
			}
		}
	}
}
