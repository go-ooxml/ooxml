// Copyright (c) the go-ooxml authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package xstring is ST_Xstring, the "escaped string" of ECMA-376 Part 1
// §22.9.2.19: text that may hold characters XML 1.0 cannot, spelled _xHHHH_.
//
// ⛔ The escape works in BOTH directions, and that is the trap. A reader that
// honours it turns the five characters "_x0041_" into "A" — so a writer that
// passes a cell whose text really is "_x0041_" straight through has written
// "A". The underscore that starts such a run is therefore escaped itself, as
// _x005F_, which is how Excel writes it.
package xstring

import (
	"strings"
	"unicode/utf8"
)

// Encode escapes s for an ST_Xstring element.
//
// A character XML 1.0 cannot carry becomes _xHHHH_; an underscore that would
// otherwise begin something a reader decodes becomes _x005F_. Invalid UTF-8
// becomes U+FFFD, because XML has no way to carry a byte that is not a
// character at all.
//
// The result still needs XML escaping (& < >): this is the layer beneath it.
func Encode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '_' && isEscape(s[i:]):
			b.WriteString("_x005F_")
		case !xmlChar(r):
			writeEscape(&b, r)
		default:
			b.WriteRune(r)
		}
		i += n
	}
	return b.String()
}

// Decode undoes [Encode]: every _xHHHH_ becomes the character it names.
func Decode(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if isEscape(s[i:]) {
			b.WriteRune(rune(hexValue(s[i+2 : i+6])))
			i += 7
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isEscape says whether s begins with _xHHHH_.
func isEscape(s string) bool {
	if len(s) < 7 || s[0] != '_' || s[1] != 'x' || s[6] != '_' {
		return false
	}
	for _, c := range []byte(s[2:6]) {
		if hexDigit(c) < 0 {
			return false
		}
	}
	return true
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func hexValue(s string) int {
	n := 0
	for _, c := range []byte(s) {
		n = n<<4 | hexDigit(c)
	}
	return n
}

const hex = "0123456789ABCDEF"

func writeEscape(b *strings.Builder, r rune) {
	b.WriteString("_x")
	b.WriteByte(hex[r>>12&0xF])
	b.WriteByte(hex[r>>8&0xF])
	b.WriteByte(hex[r>>4&0xF])
	b.WriteByte(hex[r&0xF])
	b.WriteByte('_')
}

// xmlChar is the Char production of XML 1.0 §2.2. Every character outside it
// is in the Basic Multilingual Plane, which is why four hex digits suffice.
func xmlChar(r rune) bool {
	return r == 0x09 || r == 0x0A || r == 0x0D ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}
