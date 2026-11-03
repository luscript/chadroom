package chat

import "testing"

func TestCleanNickname(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"  Ana  ", "Ana"},
		{"‮\x00", ""},
		{"   ", ""},
		{"A\nB", "AB"},
	} {
		if got := CleanNickname(tc.in); got != tc.want {
			t.Errorf("CleanNickname(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
