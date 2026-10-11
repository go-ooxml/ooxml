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

// RelHyperlink and RelImage are the relationship types a paragraph's contents
// point through.
const (
	RelHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	RelImage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	// relNumbering names word/numbering.xml, wherever the package put it.
	relNumbering = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
)

// maxDepth bounds how deeply elements may nest.
//
// ⛔ It is checked in a token pass of its own, BEFORE the document is walked,
// because the stack that a deep document overflows belongs to
// encoding/xml and not to this package: a guard placed where our own code
// recurses never runs. go-odf/odf learned that the hard way — a 115 MiB
// allocation inside the decoder, with our own depth counter at two.
const maxDepth = 256

// ParseWord reads a WordprocessingML package (.docx) into a document.
func ParseWord(src []byte, lim opc.Limits) (*richdoc.Document, error) {
	p, err := opc.Open(src, lim)
	if err != nil {
		return nil, err
	}
	main, err := p.Main()
	if err != nil {
		return nil, err
	}
	body, err := p.Part(main)
	if err != nil {
		return nil, err
	}
	if err := checkDepth(body); err != nil {
		return nil, err
	}
	w := &wordReader{pkg: p, part: main}
	if err := w.loadRels(); err != nil {
		return nil, err
	}
	if err := w.loadNumbering(); err != nil {
		return nil, err
	}
	blocks, err := w.body(body)
	if err != nil {
		return nil, err
	}
	return &richdoc.Document{Blocks: blocks}, nil
}

// checkDepth walks the tokens and nothing else, so that a document which nests
// deeper than [maxDepth] is refused before anything recurses into it.
func checkDepth(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// ⛔ A malformed stream is NOT this function's business, and
			// reporting it here was a defect found by a coverage gap. This pass
			// checks DEPTH; the walk that follows reads the same bytes and will
			// report the syntax error itself, in its own words and at the
			// element it happened on. Returning it from here did two wrong
			// things at once: it called a truncated document "nested too deep",
			// and it made every error return in every walker unreachable — one
			// pass that validates everything leaves nothing for the others to
			// find.
			return nil
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return fmt.Errorf("ooxml: elements nest more than %d deep", maxDepth)
			}
		case xml.EndElement:
			depth--
		}
	}
}

type wordReader struct {
	pkg  *opc.Package
	part string
	rels map[string]opc.Rel // by relationship id
	// numFmt is a numId's format at level zero: "bullet", "decimal", … or ""
	// when the package does not say.
	numFmt map[string]string
}

func (w *wordReader) loadRels() error {
	rels, err := w.pkg.Rels(w.part)
	if err != nil {
		return err
	}
	w.rels = make(map[string]opc.Rel, len(rels))
	for _, r := range rels {
		w.rels[r.ID] = r
	}
	return nil
}

// loadNumbering reads word/numbering.xml, which says whether a list is
// numbered or bulleted. The chain is two hops: a paragraph's w:numId names a
// w:num, which names a w:abstractNum, whose w:lvl for the level carries
// w:numFmt.
//
// ⛔ A package without it, or a numId the file never defines, is NOT an error.
// Most documents have the part; some do not, and a list whose format cannot be
// resolved is still a list. What it is not is ORDERED: see [wordReader.list].
func (w *wordReader) loadNumbering() error {
	w.numFmt = map[string]string{}
	name := "word/numbering.xml"
	// ⛔ Found through the main part's own relationships, which loadRels has
	// ALREADY read — not by asking the package again. Asking again would read
	// and parse the same part a second time, and its error branch could never
	// run, because loadRels had just succeeded on it. A coverage gap is what
	// found that dead code.
	for _, r := range w.rels {
		if !opc.SameRelType(r.Type, relNumbering) || r.External {
			continue
		}
		if n, err := w.pkg.Resolve(w.part, r); err == nil {
			name = n
		}
		break
	}
	if !w.pkg.Has(name) {
		return nil
	}
	b, err := w.pkg.Part(name)
	if err != nil {
		return err
	}
	// abstract[id] = the numFmt of its level zero; num[id] = its abstract id.
	abstract := map[string]string{}
	num := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var curAbstract, curNum string
	lvl := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "abstractNum":
				curAbstract, curNum = attr(e, "abstractNumId"), ""
			case "num":
				curNum, curAbstract = attr(e, "numId"), ""
			case "abstractNumId":
				if curNum != "" {
					num[curNum] = attr(e, "val")
				}
			case "lvl":
				lvl = attr(e, "ilvl")
			case "numFmt":
				// Only level zero: a richdoc list is ordered or not, and the
				// level a paragraph sits at is its own business.
				if curAbstract != "" && lvl == "0" {
					abstract[curAbstract] = attr(e, "val")
				}
			}
		}
	}
	for id, aid := range num {
		if f, ok := abstract[aid]; ok {
			w.numFmt[id] = f
		}
	}
	return nil
}

func (w *wordReader) body(data []byte) ([]richdoc.Block, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("ooxml: the document has no w:body")
		}
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "body" {
			return w.blocks(dec, se.Name)
		}
	}
}

// blocks reads the block-level children of an element, grouping consecutive
// list paragraphs into one list.
func (w *wordReader) blocks(dec *xml.Decoder, end xml.Name) ([]richdoc.Block, error) {
	var out []richdoc.Block
	// The list being accumulated, if any.
	var items []richdoc.ListItem
	var listID string
	flush := func() {
		if len(items) == 0 {
			return
		}
		out = append(out, richdoc.List{
			Ordered: ordered(w.numFmt[listID]),
			Items:   items,
			Tight:   true,
		})
		items, listID = nil, ""
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == end {
				flush()
				return out, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "p":
				para, err := w.paragraph(dec, e)
				if err != nil {
					return nil, err
				}
				if para.numID != "" {
					// ⛔ A list ends when the numId CHANGES, not only when a
					// non-list paragraph arrives: two lists back to back with
					// different numbering are two lists, and joining them
					// would renumber the second from where the first left off.
					if len(items) > 0 && para.numID != listID {
						flush()
					}
					listID = para.numID
					items = append(items, richdoc.ListItem{
						Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: para.inlines}},
					})
					continue
				}
				flush()
				if para.level > 0 {
					out = append(out, richdoc.Heading{Level: para.level, Inlines: para.inlines})
					continue
				}
				// ⛔ An empty paragraph is DROPPED. Word is full of them —
				// they are how a person makes vertical space — and a
				// Paragraph with no inlines reaches a PDF as a line of
				// nothing, which is not what the page showed.
				if len(para.inlines) == 0 {
					continue
				}
				out = append(out, richdoc.Paragraph{Inlines: para.inlines})
			case "tbl":
				flush()
				t, err := w.table(dec, e)
				if err != nil {
					return nil, err
				}
				out = append(out, t)
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

// ordered says whether a w:numFmt means a numbered list.
//
// ⛔ The fallback is UNORDERED, and that is a decision rather than an
// oversight. "bullet" is explicit, "decimal" and its friends are explicit, and
// "none" — which LibreOffice writes for a list whose style says nothing — is
// neither. A list presented as numbered when the file does not say so invents
// an order the document never had; presented as bulleted it loses nothing that
// was there.
func ordered(numFmt string) bool {
	switch strings.ToLower(numFmt) {
	case "", "bullet", "none":
		return false
	}
	return true
}

// paragraphInfo is what a w:p turned out to be.
type paragraphInfo struct {
	inlines []richdoc.Inline
	level   int    // a heading's level, or zero
	numID   string // the list it belongs to, or ""
}

func (w *wordReader) paragraph(dec *xml.Decoder, se xml.StartElement) (paragraphInfo, error) {
	var info paragraphInfo
	for {
		tok, err := dec.Token()
		if err != nil {
			return info, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return info, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "pPr":
				lvl, num, err := w.paragraphProps(dec, e)
				if err != nil {
					return info, err
				}
				info.level, info.numID = lvl, num
			case "r":
				ins, err := w.run(dec, e, runStyle{})
				if err != nil {
					return info, err
				}
				info.inlines = append(info.inlines, ins...)
			case "hyperlink":
				ins, err := w.hyperlink(dec, e)
				if err != nil {
					return info, err
				}
				info.inlines = append(info.inlines, ins...)
			case "ins":
				// A tracked INSERTION is part of the document: its runs are
				// read as any others. (A deletion is not; see run.)
				ins, err := w.insertion(dec, e)
				if err != nil {
					return info, err
				}
				info.inlines = append(info.inlines, ins...)
			default:
				if err := dec.Skip(); err != nil {
					return info, err
				}
			}
		}
	}
}

// paragraphProps reads a w:pPr for the two things that change what a paragraph
// IS: its heading level and the list it belongs to.
//
// ⛔ A heading's level comes from the paragraph's STYLE NAME — Heading1,
// Heading2 — and not from the element, which is the opposite of ODF's
// text:h with its outline-level attribute. There is no "heading element" in
// WordprocessingML: a heading is a paragraph wearing a style. w:outlineLvl is
// read as well, because a document may say it that way instead.
func (w *wordReader) paragraphProps(dec *xml.Decoder, se xml.StartElement) (level int, numID string, err error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, "", err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return level, numID, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "pStyle":
				if n, ok := headingLevel(attr(e, "val")); ok {
					level = n
				}
				if err := dec.Skip(); err != nil {
					return 0, "", err
				}
			case "outlineLvl":
				// ⛔ Zero-based here, one-based in richdoc: outlineLvl 0 is a
				// level-one heading. A reader that passed it through would
				// make every top heading a level zero, which no consumer has
				// a case for.
				if n, convErr := strconv.Atoi(attr(e, "val")); convErr == nil && n >= 0 && n < 9 && level == 0 {
					level = n + 1
				}
				if err := dec.Skip(); err != nil {
					return 0, "", err
				}
			case "numPr":
				id, err := w.numPr(dec, e)
				if err != nil {
					return 0, "", err
				}
				numID = id
			default:
				if err := dec.Skip(); err != nil {
					return 0, "", err
				}
			}
		}
	}
}

// headingLevel reads a style id like "Heading2".
//
// The comparison ignores case because producers differ on it, and it accepts
// the hyphenated and spaced spellings a converter may write.
func headingLevel(styleID string) (int, bool) {
	s := strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(styleID))
	rest, ok := strings.CutPrefix(s, "heading")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 || n > 9 {
		return 0, false
	}
	return n, true
}

func (w *wordReader) numPr(dec *xml.Decoder, se xml.StartElement) (string, error) {
	numID := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return numID, nil
			}
		case xml.StartElement:
			if e.Name.Local == "numId" {
				numID = attr(e, "val")
			}
			if err := dec.Skip(); err != nil {
				return "", err
			}
		}
	}
}

// runStyle is the formatting in force. It is passed down rather than read from
// a stack, so a nested structure cannot lose it.
type runStyle struct {
	bold, italic, underline, strike bool
}

func (w *wordReader) run(dec *xml.Decoder, se xml.StartElement, outer runStyle) ([]richdoc.Inline, error) {
	style := outer
	var out []richdoc.Inline
	// text accumulates this run's characters, because a run's text may arrive
	// in several w:t and they are one word.
	var text strings.Builder
	emit := func() {
		if text.Len() == 0 {
			return
		}
		out = append(out, wrap(richdoc.Text{Value: text.String()}, style))
		text.Reset()
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				emit()
				return out, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "rPr":
				s, err := runProps(dec, e)
				if err != nil {
					return nil, err
				}
				style.bold = style.bold || s.bold
				style.italic = style.italic || s.italic
				style.underline = style.underline || s.underline
				style.strike = style.strike || s.strike
			case "t":
				s, err := flatText(dec, e.Name)
				if err != nil {
					return nil, err
				}
				text.WriteString(s)
			case "br":
				emit()
				out = append(out, richdoc.LineBreak{})
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			case "tab":
				text.WriteString("\t")
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			case "drawing", "pict":
				emit()
				img, ok, err := w.drawing(dec, e)
				if err != nil {
					return nil, err
				}
				if ok {
					out = append(out, img)
				}
			case "instrText", "delText", "delInstrText":
				// ⛔ NOT text. instrText is a field's instructions — "PAGE \*
				// MERGEFORMAT" — which a reader shows as a page number and
				// never as those words. delText is text somebody DELETED with
				// track changes on: it is in the file so the change can be
				// rejected, and emitting it puts removed sentences back into
				// the document, which is a confidentiality problem before it
				// is a correctness one.
				//
				// ⛔⛔ TWO MUTATIONS SURVIVED HERE, AND BETWEEN THEM THEY SAY
				// something true about the code rather than about the tests.
				// Deleting this case changes no test, because `default` below
				// drops what this walker does not name. Making `default` emit
				// the text changes no test either, because this case catches
				// these three names first. Two independent mechanisms, each
				// sufficient, so a mutation of ONE is masked by the other —
				// which is what defence in depth looks like from a mutation
				// report, and is not a weak test.
				//
				// So the case stays for the two things it does that `default`
				// cannot: it NAMES these three, so their absence from the
				// output reads as deliberate rather than forgotten, and it
				// keeps working if `default` is ever changed. ⛔ What no
				// mutation of a single line can establish is the pair being
				// wrong at once — that is what the tests are for, and
				// TestDeletedTextStaysDeletedAndInsertedTextArrives asserts
				// the OUTPUT rather than the mechanism, which is the only
				// assertion that survives either one being rewritten.
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

// wrap puts the formatting round a piece of text, innermost first.
func wrap(in richdoc.Inline, s runStyle) richdoc.Inline {
	if s.strike {
		in = richdoc.Strikethrough{Inlines: []richdoc.Inline{in}}
	}
	if s.italic {
		in = richdoc.Emph{Inlines: []richdoc.Inline{in}}
	}
	if s.bold {
		in = richdoc.Strong{Inlines: []richdoc.Inline{in}}
	}
	// ⛔ Underline has no richdoc inline of its own and is deliberately NOT
	// mapped to Emph or Strong. Underlined text is not emphasised text — in a
	// legal document it is a defined term, in a web page it is a link that is
	// not one — and borrowing another meaning for it would make a document
	// that says something it does not.
	return in
}

func runProps(dec *xml.Decoder, se xml.StartElement) (runStyle, error) {
	var s runStyle
	for {
		tok, err := dec.Token()
		if err != nil {
			return s, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return s, nil
			}
		case xml.StartElement:
			// ⛔ w:val="0" or "false" TURNS IT OFF. A toggle property with no
			// val is on; with a val it is what the val says. A reader that
			// treated the element's presence as "on" would make bold every
			// run whose style switched bold off, which is how a style
			// inherited from a heading leaks into its own body text.
			on := toggle(e)
			switch e.Name.Local {
			case "b", "bCs":
				s.bold = s.bold || on
			case "i", "iCs":
				s.italic = s.italic || on
			case "u":
				s.underline = s.underline || attr(e, "val") != "none"
			case "strike", "dstrike":
				s.strike = s.strike || on
			}
			if err := dec.Skip(); err != nil {
				return s, err
			}
		}
	}
}

func toggle(se xml.StartElement) bool {
	switch strings.ToLower(attr(se, "val")) {
	case "0", "false", "off":
		return false
	}
	return true
}

func (w *wordReader) hyperlink(dec *xml.Decoder, se xml.StartElement) ([]richdoc.Inline, error) {
	url := ""
	if id := attrNS(se, "id"); id != "" {
		if r, ok := w.rels[id]; ok {
			// ⛔ An external relationship's Target is the URL ITSELF and must
			// not be resolved to a part. Measured: LibreOffice writes a
			// hyperlink as TargetMode="External", so opc.Resolve refuses it —
			// correctly — and a reader that called Resolve here would lose
			// every link in the document.
			if r.External {
				url = r.Target
			} else if name, err := w.pkg.Resolve(w.part, r); err == nil {
				url = name
			}
		}
	}
	if anchor := attr(se, "anchor"); url == "" && anchor != "" {
		url = "#" + anchor
	}
	var inner []richdoc.Inline
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				if url == "" {
					// A hyperlink that points nowhere is its text. Emitting a
					// Link with an empty URL would make a PDF with a dead
					// clickable region on it.
					return inner, nil
				}
				return []richdoc.Inline{richdoc.Link{URL: url, Inlines: inner}}, nil
			}
		case xml.StartElement:
			if e.Name.Local == "r" {
				ins, err := w.run(dec, e, runStyle{})
				if err != nil {
					return nil, err
				}
				inner = append(inner, ins...)
				continue
			}
			if err := dec.Skip(); err != nil {
				return nil, err
			}
		}
	}
}

// insertion reads a w:ins, whose runs are ordinary content.
func (w *wordReader) insertion(dec *xml.Decoder, se xml.StartElement) ([]richdoc.Inline, error) {
	var out []richdoc.Inline
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return out, nil
			}
		case xml.StartElement:
			if e.Name.Local == "r" {
				ins, err := w.run(dec, e, runStyle{})
				if err != nil {
					return nil, err
				}
				out = append(out, ins...)
				continue
			}
			if err := dec.Skip(); err != nil {
				return nil, err
			}
		}
	}
}

// drawing finds the picture a w:drawing or w:pict places, by the a:blip (or
// v:imagedata) that names its relationship.
func (w *wordReader) drawing(dec *xml.Decoder, se xml.StartElement) (richdoc.Image, bool, error) {
	var img richdoc.Image
	found := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return img, false, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				return img, found, nil
			}
		case xml.StartElement:
			switch e.Name.Local {
			case "blip", "imagedata":
				id := attrNS(e, "embed")
				if id == "" {
					id = attrNS(e, "id")
				}
				if r, ok := w.rels[id]; ok && !r.External {
					if name, err := w.pkg.Resolve(w.part, r); err == nil {
						img.URL, found = name, true
					}
				} else if ok {
					img.URL, found = r.Target, true
				}
			case "docPr", "cNvPr":
				// The accessible name and description of the picture live
				// here, as attributes rather than as elements.
				if img.Alt == "" {
					img.Alt = attr(e, "descr")
				}
				if img.Title == "" {
					img.Title = attr(e, "name")
				}
			}
			if err := dec.Skip(); err != nil {
				return img, false, err
			}
		}
	}
}

func (w *wordReader) table(dec *xml.Decoder, se xml.StartElement) (richdoc.Block, error) {
	var rows [][]richdoc.Cell
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.EndElement:
			if e.Name == se.Name {
				t := richdoc.Table{}
				// ⛔ The first row is a HEADER only when the file says so.
				// Word marks it with w:tblHeader in the row's properties;
				// taking the first row regardless would turn a two-row table
				// of data into one row of data under a heading that is data.
				t.Rows = rows
				return t, nil
			}
		case xml.StartElement:
			if e.Name.Local == "tr" {
				cells, err := w.row(dec, e)
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

func (w *wordReader) row(dec *xml.Decoder, se xml.StartElement) ([]richdoc.Cell, error) {
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
				blocks, err := w.blocks(dec, e.Name)
				if err != nil {
					return nil, err
				}
				cells = append(cells, richdoc.Cell{Blocks: blocks})
				continue
			}
			if err := dec.Skip(); err != nil {
				return nil, err
			}
		}
	}
}

// flatText reads the character data of an element and nothing else.
//
// ⛔ It does NOT trim. w:t carries xml:space="preserve" precisely because the
// spaces at its ends are part of the sentence: "plain then " followed by
// "bold" is three words, and trimmed it is two run together.
func flatText(dec *xml.Decoder, end xml.Name) (string, error) {
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch e := tok.(type) {
		case xml.CharData:
			sb.Write(e)
		case xml.EndElement:
			if e.Name == end {
				return sb.String(), nil
			}
		}
	}
}

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// attrNS reads an attribute that must be namespaced — r:id, r:embed — so that
// a plain "id" on the same element is not mistaken for one.
func attrNS(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local && a.Name.Space != "" {
			return a.Value
		}
	}
	return ""
}
