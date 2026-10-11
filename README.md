# ooxml

**Word, Excel and PowerPoint into [richdoc](https://github.com/go-richdoc/richdoc).**
Pure Go, CGO-free, including `GOOS=js`. The container is
[`go-ooxml/opc`](https://github.com/go-ooxml/opc); the only other dependency is
richdoc itself.

```go
doc, err := ooxml.ParseWord(docx, opc.Limits{})   // .docx → *richdoc.Document
```

Word is read today. Excel and PowerPoint are next, and the reason they are
separate readers rather than one is the first thing worth knowing about this
format.

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

## ⛔ A heading is a paragraph wearing a style

There is no heading element in WordprocessingML. A heading is a `w:p` whose
`w:pStyle` names `Heading1`…`Heading9` — which is the opposite of ODF's
`text:h` with its `outline-level` attribute. `w:outlineLvl` is read as well,
because a document may say it that way instead, and it is **zero-based** where
richdoc is one-based: passing it through would make every top heading a level
zero.

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
