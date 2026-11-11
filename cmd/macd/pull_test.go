package main

import (
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

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

func TestWithCredentialHelperPath(t *testing.T) {
	got := withCredentialHelperPath("/usr/bin:/bin:/opt/homebrew/bin", "/Users/me")
	want := "/usr/bin:/bin:/opt/homebrew/bin:/Users/me/.docker/bin:/Applications/Docker.app/Contents/Resources/bin:/usr/local/bin"
	if got != want {
		t.Fatalf("withCredentialHelperPath = %q, want %q", got, want)
	}
	if again := withCredentialHelperPath(got, "/Users/me"); again != got {
		t.Fatalf("second call changed PATH to %q", again)
	}
}

type failingKeychain struct{}

func (failingKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	return nil, errors.New(`exec: "docker-credential-desktop": executable file not found in $PATH`)
}

func TestAnonymousFallbackWhenCredentialHelperFails(t *testing.T) {
	registry, err := name.NewRegistry("index.docker.io")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := anonymousFallback{failingKeychain{}}.Resolve(registry)
	if err != nil || auth != authn.Anonymous {
		t.Fatalf("Resolve = %v, %v; want anonymous", auth, err)
	}
}
