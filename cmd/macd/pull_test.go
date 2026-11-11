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

func TestImageForLockedMatchesShortNames(t *testing.T) {
	pulled := &storedImage{tag: "docker.io/jtstormz/tiny-web:llama-server-001", id: "sha256:pulled"}
	official := &storedImage{tag: "docker.io/library/busybox:latest", id: "sha256:official"}
	local := &storedImage{tag: "tiny-web:latest", id: "sha256:local"}
	d := &daemon{images: map[string]*storedImage{pulled.tag: pulled, official.tag: official, local.tag: local}}
	for _, tc := range []struct {
		name string
		want *storedImage
	}{
		{"docker.io/jtstormz/tiny-web:llama-server-001", pulled},
		{"jtstormz/tiny-web:llama-server-001", pulled},
		{"index.docker.io/jtstormz/tiny-web:llama-server-001", pulled},
		{"busybox", official},
		{"library/busybox:latest", official},
		{"tiny-web", local},
		{"docker.io/library/tiny-web:latest", local},
		{"sha256:local", local},
		{"jtstormz/tiny-web:dev-002", nil},
		{"jtstormz/tiny-web", nil},
		{"other.example.com/jtstormz/tiny-web:llama-server-001", nil},
	} {
		if got := d.imageForLocked(tc.name); got != tc.want {
			t.Errorf("imageForLocked(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
