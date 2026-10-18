// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package ooxml reads Office Open XML documents into the neutral
// [github.com/go-richdoc/richdoc] model — Word (.docx), Excel (.xlsx) and
// PowerPoint (.pptx) — and writes them: [WriteWord] and [WriteWorkbook] from
// a document, [WritePresentation] from a deck of rendered slides.
//
// # One container, three vocabularies
//
// ⛔ This is the opposite of ODF, and the difference decides the shape of the
// package. In OpenDocument, office:text, office:spreadsheet,
// office:presentation and office:drawing are four BODIES that share one
// vocabulary: text:p is a paragraph in all of them, so one walker reads all
// four. In OOXML the three formats share the CONTAINER — the ZIP, the content
// types, the relationships, all of which are [github.com/go-ooxml/opc] — and
// then speak three different languages:
//
//	Word         w:p > w:r > w:t                    (WordprocessingML)
//	PowerPoint   a:p > a:r > a:t                    (DrawingML, inside p:sp)
//	Excel        c > v, where v may be an INDEX      (SpreadsheetML)
//	             into xl/sharedStrings.xml
//
// So there is one reader per format here, and the thing they share lives one
// repository down.
package ooxml
