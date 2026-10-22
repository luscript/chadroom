package chat

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeNickname makes an untrusted nickname safe to display: valid UTF-8,
// control and bidi-control characters removed, trimmed, at most
// maxNicknameRunes runes, "Anonymous" if empty.
func normalizeNickname(s string) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, s)
	s = truncateRunes(strings.TrimSpace(s), maxNicknameRunes)
	if s == "" {
		return "Anonymous"
	}
	return s
}

// truncateRunes keeps the first n runes of s and trims trailing whitespace
// left by the cut.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// foldKey maps each rune to the smallest rune of its SimpleFold orbit, the
// same equivalence strings.EqualFold uses, so keys are equal iff names are
// equal ignoring case.
func foldKey(s string) string {
	return strings.Map(func(r rune) rune {
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < lowest {
				lowest = f
			}
		}
		return lowest
	}, s)
}

// assignNickname returns base if free, else the lowest free "base (n)", n >= 2,
// with base truncated so the result fits maxNicknameRunes. Run-only: reads taken.
func (cr *Room) assignNickname(base string) string {
	if _, ok := cr.taken[foldKey(base)]; !ok {
		return base
	}
	for n := 2; ; n++ {
		suffix := " (" + strconv.Itoa(n) + ")"
		cand := truncateRunes(base, maxNicknameRunes-utf8.RuneCountInString(suffix)) + suffix
		if _, ok := cr.taken[foldKey(cand)]; !ok {
			return cand
		}
	}
}
