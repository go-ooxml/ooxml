# ooxml

**Word, Excel and PowerPoint into [richdoc](https://github.com/go-richdoc/richdoc).**
Pure Go, CGO-free, including `GOOS=js`. The container is
[`go-ooxml/opc`](https://github.com/go-ooxml/opc); the only other dependency is
richdoc itself.

```go
doc, err := ooxml.ParseWord(docx, opc.Limits{})          // .docx
deck, err := ooxml.ParsePresentation(pptx, opc.Limits{}) // .pptx
book, err := ooxml.ParseWorkbook(xlsx, opc.Limits{})     // .xlsx
```

and back out again:

```go
docx, err := ooxml.WriteWord(doc)       // headings, paragraphs, lists, tables
xlsx, err := ooxml.WriteWorkbook(doc)   // the document's tables, one per sheet
pptx, err := ooxml.WritePresentation(p) // one picture per slide, with speaker notes
```

Word, PowerPoint and Excel are all read and all written. The reason
the readers are separate rather than one is the first thing worth knowing about
this format.

## Writing one

⛔ **What comes back is a package, not a rendering.** Word decides how wide a
column is, where a page breaks and what a heading looks like. This says which
paragraph is a heading and which cells are a row, and the styles part gives
those names something to mean.

⛔ **The heading styles are defined, not left to Word's latent ones.** A
`w:pStyle` naming a style the package does not define works in Word, which
knows `Heading1` already — and shows as plain text in a reader that does not,
which is most of them. Defining them costs a few hundred bytes and is the
difference between a heading and a paragraph everywhere but Word.

⛔ **It is not a round trip with `ParseWord`.** A document read from Word and
written back loses everything richdoc has no word for — the fonts, the margins,
the section properties, the revision history — because richdoc is a *document*
model and not a .docx model. Said here because "write what you read" is the
obvious thing to expect and it is not what this does.

⛔ **`WriteWorkbook` writes only the tables.** A spreadsheet is a grid of cells
and a document is not, so the prose around a table has nowhere to go — and
putting it in column A of a sheet of its own would make a file that looks like
a document and works as neither. A document with no tables gives one empty
sheet, because "there was nothing to put in a grid" is an answer.

Three things the file must get right, where the program refuses the whole
document rather than the part:

| | |
|---|---|
| every cell needs a paragraph | a `w:tc` with nothing in it is what makes Word call a document corrupt, so a short row is filled out and an empty cell still gets one |
| `xml:space="preserve"` on every run | without it a reader that follows the standard trims a run, and a document rebuilt from runs that each lost their edges reads as onewordafteranother |
| a sheet name is built, never taken | Excel refuses a name over 31 characters, one holding `: \ / ? * [ ]`, or a duplicate — and it refuses the **file**, not the name |

### ⛔ The judge for a writer cannot be its own reader

Almost every test writes a package and reads it back with `ParseWord`,
`ParseWorkbook` or `ParsePresentation`. That proves the two agree, and **two halves of one package
agreeing is the weakest evidence there is**: a file our reader is delighted
with can be a file Word refuses, and that failure turns up in somebody's hands.

Three things turned out to be invisible to the round trip, because our reader
is more forgiving than the standard — an `Override`'s absolute `PartName`, the
`.rels` Default, and `xml:space` — so those are asserted by their **spelling**,
with a `.docx` Word itself produced as the control.

And where **LibreOffice** is installed it is asked, which is a reader other
people wrote from the same standard. Measured 2026-10-12, on a 2-page PDF read
through `go-pdfkit/document` and written out here:

```text
soffice --convert-to txt out.docx →  headings, paragraphs, and bullets indented
soffice --convert-to csv out.xlsx →  Activité RFCM …,Réalisation
```

That test **skips** where LibreOffice is not installed, which includes CI. A
skipped test is not a gate, which is why the result is written down here with
its date rather than left to imply one.

### A deck is written as pictures

```go
pptx, err := ooxml.WritePresentation(ooxml.Presentation{
	Width: 1280 * 9525, Height: 720 * 9525, // EMU: a CSS pixel is 9525
	Title: "Deck", Author: "Ada", Subject: "…", Keywords: "a, b",
	Slides: []ooxml.PresentationSlide{
		{Image: png1, ImageType: "png", Notes: "what to say", Name: "Opening"},
	},
})
```

This is what `marp-cli --pptx` writes, and it is for the same consumer: a
renderer whose output **is** the slide. Each slide is one full-slide `p:pic`
over a blank layout, its `Name` is the picture's alternative text, and its
`Notes` go to a notes slide — written only for a slide that has notes, as
ECMA-376 §13.3.5 asks.

⛔ **The output is deterministic.** No clock is read: `Created` is written to
`dcterms:created` and `modified` only when given, and every id is a position.

⛔ **The ids are the standard's ranges, not counters from 1.** A slide id is
256 or more (`ST_SlideId`), a master's or layout's 2³¹ or more, and the master
and its layouts share that space.

⛔ **The bounds are the reader's.** Slide size 914 400 – 51 206 400 EMU
(`ST_SlideSizeCoordinate`), at most 1000 slides, 64 MiB a picture and 192 MiB
of pictures in all, 1 MiB a text — so what this writes, `ParsePresentation`
reads under the default `opc.Limits`. A picture whose signature is not its
`ImageType` is refused: the type becomes the part's content type, and a JPEG
called PNG is a blank slide.

The part set is the one §13.3 requires — one master, one layout, a theme, the
presentation properties — plus the notes master, view properties and table
styles that marp-cli 4.5.1's output carries. ParsePresentation reads the
slides back and **none of their words**: the words are in the pictures.
Measured 2026-10-18 with LibreOffice: a three-slide deck converts to a
three-page PDF, each page the picture edge to edge, and to an `.odp` whose
`presentation:notes` hold the notes.

## ⛔ One container, three vocabularies — the opposite of ODF

In OpenDocument, `office:text`, `office:spreadsheet`, `office:presentation` and
`office:drawing` are four **bodies that share one vocabulary**: `text:p` is a
paragraph in all of them, so one walker reads all four. In OOXML the three
formats share the **container** — the ZIP, the content types, the
relationships, all of which live one repository down — and then speak three
different languages:

| | the text is |
| --- | --- |
| Word | `w:p > w:r > w:t` (WordprocessingML) |
| PowerPoint | `a:p > a:r > a:t` (DrawingML, inside a `p:sp` shape) |
| Excel | `c > v`, where `v` may be an **index** into `xl/sharedStrings.xml` |

So there is one reader per format here. The thing they share is `opc`.


## PowerPoint: the order is the list's

```go
doc, err := ooxml.ParsePresentation(pptx, opc.Limits{})
```

Each slide comes out as a heading of its **position**, followed by the text in
its shapes.

⛔ **The order comes from `p:sldIdLst`, not from the filenames.**
`slide1.xml`, `slide2.xml` … are in order in a file straight out of a
converter, and stop being in order the moment somebody moves a slide: the names
stay where they were and the list changes. A reader that sorted by filename
would silently reorder the deck, and it would pass every test written against a
converted fixture — which is why there is a test whose list is the *reverse* of
its filenames.

The heading is the slide's position and not a title taken from a shape: a
`.pptx` carries no name for a slide, and the placeholder that would say which
shape is the title is absent from a converted deck — the same thing `go-odf`
found for `.odp`, where `presentation:class` is stripped.

### ⛔⛔ DrawingML carries its formatting as ATTRIBUTES

This is the trap that makes a shared implementation wrong:

| | a bold run is |
| --- | --- |
| WordprocessingML | `<w:rPr><w:b/></w:rPr>` — a child **element** |
| DrawingML | `<a:rPr b="1"/>` — an **attribute** |

A reader that reused Word's `runProps` here would find **no formatting at all**
in a `.pptx`, because every child of `a:rPr` is a colour or a font and none of
them is a toggle.

And two of those attributes are **named values, not booleans**: `u="none"` and
`strike="noStrike"` are *off*. Measured: LibreOffice writes
`b="0" strike="noStrike" u="none"` on every run of a converted deck, so a
reader treating the attribute's presence as "on" would make the whole deck
bold, underlined and struck through at once.

### A field means the opposite of Word's

A slide's `a:fld` holds in its `a:t` the value a viewer **showed** — the page
number, the date — so it is read. Word's `w:instrText` holds the
**instructions** and is dropped. The two formats put opposite things in the
same place, which is one more reason the readers are separate.

### Bullets are what the file says

`a:buChar` is a bulleted item, `a:buAutoNum` a numbered one, and ⛔ `a:buNone`
says explicitly that a paragraph is **not** one — which has to be honoured,
because a converted deck puts `marL` and `indent` on every paragraph of a text
box, so an indent alone is not a bullet.

## ⛔ A heading is a paragraph wearing a style

There is no heading element in WordprocessingML. A heading is a `w:p` whose
`w:pStyle` names `Heading1`…`Heading9` — which is the opposite of ODF's
`text:h` with its `outline-level` attribute. `w:outlineLvl` is read as well,
because a document may say it that way instead, and it is **zero-based** where
richdoc is one-based: passing it through would make every top heading a level
zero.


## Excel: a cell's value is often not in the cell

```go
book, err := ooxml.ParseWorkbook(xlsx, opc.Limits{})
```

Each sheet comes out as a heading of its own **name** — `Q3 actuals` from
`xl/workbook.xml`, not `sheet2.xml` from the disk — followed by its cells as a
table.

⛔ **With `t="s"`, which is what every piece of text in a real workbook uses,
the `<v>` holds an INDEX into `xl/sharedStrings.xml`.** A reader that took
`<v>` as the text produces a table of `0`, `1`, `2` … in place of every word —
and it *looks like a working reader*, because small integers are plausible cell
contents in a spreadsheet. Measured on a file LibreOffice wrote from a
three-line CSV: every one of its five text cells was an index.

An index the shared table does not hold yields **nothing**, not the number: a
workbook whose strings are missing should come out with empty cells rather than
with the indexes in place of its words.

⛔ **A cell is placed by its reference, not by its position among its
siblings.** A row writes only the cells that hold something — `<c r="A1"/>`
then `<c r="C1"/>` for a row whose B is empty — so a reader that appended in
order would shift C into B's place and every column after it. That is the
defect which shows up as *"the numbers are in the wrong columns"* on somebody's
real spreadsheet and on **no fixture**.

| `t=` | the value is |
| --- | --- |
| `s` | an index into the shared strings |
| `inlineStr` | in `<is><t>` on the cell itself |
| `str` | a formula's string result, in `<v>` |
| `b` | 0 or 1 in `<v>`, shown as FALSE or TRUE |
| `e` | an error like `#DIV/0!`, in `<v>` |
| `n` or absent | a number in `<v>` — Excel omits `t`, LibreOffice writes it, so both must work |

The `<f>` beside a value is the **formula** and is dropped: `=SUM(A1:A9)` is
what the cell computes with, and the number is what it showed.

A **phonetic hint** (`rPh`) is stored beside a word and never shown in the
cell; its `<t>` would otherwise be appended to the string.

## ⛔ Four things in the file that must not reach the document

| | |
| --- | --- |
| `w:instrText` | a field's **instructions**. `PAGE \* MERGEFORMAT` is what the file says and `7` is what a reader shows; emitting the instructions puts typesetting directives into the prose |
| `w:delText` | text somebody **deleted** with track changes on. It is in the file so the change can be rejected, and emitting it puts removed sentences back into the document — a confidentiality problem before it is a correctness one |
| `w:b w:val="0"` | a toggle property **switched off**. Reading the element's presence as "on" makes bold every run whose style switched bold off |
| an empty `w:p` | Word is full of them; they are how a person makes vertical space. A paragraph with no inlines reaches a PDF as a line of nothing |

`w:ins` is the other half of the third: a tracked **insertion** is part of the
document, and its runs are read as any others.

## What the fixtures proved, and what they could not

The `.docx` in `testdata` was **written by LibreOffice**. Two rules come
straight from reading it:

- a **hyperlink arrives as an external relationship**, so its `Target` is the
  URL itself. `opc.Resolve` refuses an external target — correctly — and a
  reader that called it here would lose every link in the document;
- `numbering.xml` said `numFmt="none"` for a list whose style said nothing. So
  the fallback for a list whose format the file does not give is
  **unordered**: a list presented as numbered invents an order the document
  never had, while presented as bulleted it loses nothing that was there.

⛔ The hand-made fixtures are for the branches a converter will not produce on
demand — a field code, a tracked deletion, a toggle switched off. **Discovering
a rule needs a real file; exercising a branch does not**, and the two kinds of
fixture are in separate files so the distinction cannot blur.

## Underline is not emphasis

Underlined text has no richdoc inline of its own and is deliberately **not**
mapped to `Emph` or `Strong`. In a legal document an underline is a defined
term; in a web page it is a link that is not one. Borrowing another meaning for
it would make a document that says something it does not.

## The first row is not a header

Word marks a header row with `w:tblHeader`. Taking the first row regardless
would turn a two-row table of data into one row of data under a heading that is
data.

## Checks

100 % statement coverage with **zero** uncovered blocks, race-clean, `go vet`
and `gofmt` clean, ten cross-compile targets including `js/wasm`,
`linux/loong64` and `linux/s390x`.

⛔ Two defects were found by the coverage gaps rather than by a test, and both
were real:

- the depth guard, a token pass of its own, was **also reporting syntax
  errors**. That called a truncated document "nested too deep", and it made
  every error return in every walker unreachable — one pass that validates
  everything leaves nothing for the others to find. Coverage went from 87.9 %
  to 98.7 % when it was given back its one job;
- `loadNumbering` asked the package for relationships that `loadRels` had
  already read, so its error branch could never run. It uses the ones in hand
  now, which is also one less part to parse twice.

## Licence

BSD-3-Clause.

## What the mutations said

Seven, five caught. ⛔ **Two survived, and between them they say something true
about the code rather than about the tests.** Deleting the
`instrText`/`delText` case changes no test, because `default` drops what the
walker does not name; making `default` emit the text changes no test either,
because that case catches those names first. Two independent mechanisms, each
sufficient, so a mutation of **one** is masked by the other — which is what
defence in depth looks like from a mutation report, and is not a weak test.

What no mutation of a single line can establish is the pair being wrong at
once. That is what
`TestDeletedTextStaysDeletedAndInsertedTextArrives` is for: it asserts the
**output** rather than the mechanism, which is the only assertion that
survives either one being rewritten.
