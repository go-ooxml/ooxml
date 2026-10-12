// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package ooxml

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ⛔ THE ONLY JUDGE HERE THAT IS NOT OUR OWN CODE.
//
// Every other test writes a package and reads it back with ParseWord or
// ParseWorkbook — which proves the two agree, and two halves of one package
// agreeing is the weakest kind of evidence there is. A file that our reader is
// delighted with can be a file Word refuses to open, and that failure turns up
// in somebody's hands rather than here.
//
// So where LibreOffice is installed, it is asked. It is not Word, and it is
// more forgiving than Word — but it is a reader written by other people from
// the same standard, and it has refused OOXML we got wrong before.
//
// It SKIPS where LibreOffice is not installed, which includes CI. A skipped
// test is not a check, so the README says what this proved and when rather
// than implying the gate enforces it.
func TestLibreOfficeOpensWhatWeWrote(t *testing.T) {
	soffice := findSoffice()
	if soffice == "" {
		t.Skip("no LibreOffice here: the round trip through our own reader is the only judge")
	}
	dir := t.TempDir()
	docx := filepath.Join(dir, "out.docx")
	xlsx := filepath.Join(dir, "out.xlsx")
	word, err := WriteWord(sample())
	if err != nil {
		t.Fatal(err)
	}
	book, err := WriteWorkbook(sample())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docx, word, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xlsx, book, 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in, to, out string
		wants       []string
	}{
		{docx, "txt", "out.txt", []string{"The Title", "A Section", "one", "Ada", "41"}},
		{xlsx, "csv", "out.csv", []string{"Name", "Age", "Ada", "36"}},
	}
	for _, c := range cases {
		cmd := exec.Command(soffice, "--headless", "--convert-to", c.to, "--outdir", dir, c.in)
		// LibreOffice writes its profile under HOME, and a first run with
		// none takes a while; t.TempDir keeps it out of the way.
		cmd.Env = append(os.Environ(), "HOME="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("LibreOffice refused %s: %v\n%s", filepath.Base(c.in), err, out)
		}
		got, err := os.ReadFile(filepath.Join(dir, c.out))
		if err != nil {
			t.Fatalf("LibreOffice wrote nothing for %s: %v", filepath.Base(c.in), err)
		}
		for _, want := range c.wants {
			if !strings.Contains(string(got), want) {
				t.Errorf("%s: LibreOffice did not read %q:\n%s", filepath.Base(c.in), want, got)
			}
		}
	}
}

// findSoffice is where LibreOffice is, or "" if it is not here. The macOS
// bundle is not on PATH, which is why the name alone is not enough.
func findSoffice() string {
	if p, err := exec.LookPath("soffice"); err == nil {
		return p
	}
	if runtime.GOOS == "darwin" {
		const bundled = "/Applications/LibreOffice.app/Contents/MacOS/soffice"
		if _, err := os.Stat(bundled); err == nil {
			return bundled
		}
	}
	return ""
}
