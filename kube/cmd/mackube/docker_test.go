package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDockerCreatePassesArgsAndBinds(t *testing.T) {
	client := &dockerClient{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/containers/create" || r.URL.Query().Get("name") != "test" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var body struct {
			Image      string   `json:"Image"`
			Cmd        []string `json:"Cmd"`
			HostConfig struct {
				Binds []string `json:"Binds"`
			} `json:"HostConfig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode create request: %v", err)
		}
		if body.Image != "llama-server:local" || !reflect.DeepEqual(body.Cmd, []string{"-m", "models/model.gguf"}) || !reflect.DeepEqual(body.HostConfig.Binds, []string{"/host/models:/app/models:ro"}) {
			t.Errorf("unexpected create body: %+v", body)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"Id":"container-id"}`)),
		}, nil
	})}}
	id, err := client.create(context.Background(), "llama-server:local", "test", []string{"-m", "models/model.gguf"}, []string{"/host/models:/app/models:ro"})
	if err != nil || id != "container-id" {
		t.Fatalf("create = %q, %v", id, err)
	}
}
