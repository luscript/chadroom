package chat

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// cleanText makes untrusted text safe to display: valid UTF-8, control and
// bidi-control characters removed (newlines included), trimmed, at most
// maxRunes runes. It may return "".
func cleanText(s string, maxRunes int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, s)
	return truncateRunes(strings.TrimSpace(s), maxRunes)
}

// cleanNickname is cleanText limited to maxNicknameRunes. It may return "".
func cleanNickname(s string) string {
	return cleanText(s, maxNicknameRunes)
}

// normalizeNickname is cleanNickname with "Anonymous" for an empty result.
func normalizeNickname(s string) string {
	if s = cleanNickname(s); s == "" {
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
