package main

import "testing"

func TestPullTag(t *testing.T) {
	for _, tc := range []struct {
		from, tag, want string
		invalid         bool
	}{
		{"jtstormz/tiny-web", "dev-00", "jtstormz/tiny-web:dev-00", false},
		{"jtstormz/tiny-web", "", "jtstormz/tiny-web:latest", false},
		{"localhost:5000/tiny-web", "dev", "localhost:5000/tiny-web:dev", false},
		{"jtstormz/tiny-web:dev", "", "jtstormz/tiny-web:dev", false},
		{"jtstormz/tiny-web:dev", "latest", "", true},
		{"", "", "", true},
	} {
		got, err := pullTag(tc.from, tc.tag)
		if (err != nil) != tc.invalid || got != tc.want {
			t.Errorf("pullTag(%q, %q) = %q, %v; want %q, invalid=%t", tc.from, tc.tag, got, err, tc.want, tc.invalid)
		}
	}
}
