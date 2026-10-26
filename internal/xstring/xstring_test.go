// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

package xstring

import (
	"testing"
	"unicode/utf8"
)

func TestEncode(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"", ""},
		// §22.9.2.19's own example: character 8 is not XML.
		{"a\bb", "a_x0008_b"},
		{"\x00", "_x0000_"},
		{"\t\n\r", "\t\n\r"},
		{"￾￿", "_xFFFE__xFFFF_"},
		// ⛔ Text that LOOKS like an escape is escaped, so it reads back as
		// itself and not as the character it spells.
		{"_x0041_", "_x005F_x0041_"},
		{"_x004G_", "_x004G_"},
		{"_x0041", "_x0041"},
		{"a_b", "a_b"},
		{"bad\xffutf8", "bad�utf8"},
		{"é😀", "é😀"},
	} {
		if got := Encode(c.in); got != c.want {
			t.Errorf("Encode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecode(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"a_x0008_b", "a\bb"},
		{"_x005F_x0041_", "_x0041_"},
		{"_x00e9_", "é"},
		{"_xZZZZ_", "_xZZZZ_"},
		{"_x12", "_x12"},
	} {
		if got := Decode(c.in); got != c.want {
			t.Errorf("Decode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// FuzzRoundTrip: whatever the text, decoding what was encoded gives it back —
// once invalid UTF-8 has become U+FFFD, which is the one change Encode makes on
// purpose.
func FuzzRoundTrip(f *testing.F) {
	for _, s := range []string{"", "_x0041_", "a\x01b", "_x005F_", "\xff", "__x0000__"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		want := []rune(s)
		enc := Encode(s)
		for _, r := range enc {
			if !xmlChar(r) {
				t.Fatalf("Encode(%q) = %q holds %U, which XML cannot carry", s, enc, r)
			}
		}
		if got := []rune(Decode(enc)); string(got) != string(want) {
			t.Fatalf("Decode(Encode(%q)) = %q", s, string(got))
		}
		if !utf8.ValidString(enc) {
			t.Fatalf("Encode(%q) is not UTF-8", s)
		}
	})
}
