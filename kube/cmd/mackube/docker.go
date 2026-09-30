package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type dockerClient struct {
	http *http.Client
}

type dockerError struct {
	Message string `json:"message"`
}

type dockerAPIError struct {
	Method, Path, Message string
	Status                int
}

func (e *dockerAPIError) Error() string {
	return fmt.Sprintf("macd %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

func dockerNotFound(err error) bool {
	var apiErr *dockerAPIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

type dockerInspect struct {
	ID    string `json:"Id"`
	Image string `json:"Image"`
	State struct {
		Status    string    `json:"Status"`
		Running   bool      `json:"Running"`
		ExitCode  int       `json:"ExitCode"`
		StartedAt time.Time `json:"StartedAt"`
	} `json:"State"`
}

func newDockerClient(socket string) *dockerClient {
	return &dockerClient{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

func (d *dockerClient) call(ctx context.Context, method, path string, body any, out any, accepted ...int) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://macd"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	for _, code := range accepted {
		if response.StatusCode == code {
			if out == nil {
				return nil
			}
			return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out)
		}
	}
	var detail dockerError
	_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&detail)
	return &dockerAPIError{Method: method, Path: path, Status: response.StatusCode, Message: detail.Message}
}

func (d *dockerClient) ping(ctx context.Context) error {
	return d.call(ctx, http.MethodGet, "/_ping", nil, nil, http.StatusOK)
}

func (d *dockerClient) runningCount(ctx context.Context) (int, error) {
	var items []json.RawMessage
	err := d.call(ctx, http.MethodGet, "/containers/json", nil, &items, http.StatusOK)
	return len(items), err
}

func (d *dockerClient) imageExists(ctx context.Context, image string) error {
	return d.call(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil, &struct{}{}, http.StatusOK)
}

func (d *dockerClient) create(ctx context.Context, image, name string, args, binds []string) (string, error) {
	var response struct {
		ID string `json:"Id"`
	}
	path := "/containers/create?name=" + url.QueryEscape(name)
	err := d.call(ctx, http.MethodPost, path, map[string]any{
		"Image": image, "Cmd": args,
		"HostConfig": map[string]any{"Binds": binds},
	}, &response, http.StatusCreated)
	return response.ID, err
}

func (d *dockerClient) start(ctx context.Context, id string) error {
	return d.call(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, http.StatusNoContent)
}

func (d *dockerClient) inspect(ctx context.Context, name string) (*dockerInspect, error) {
	var result dockerInspect
	err := d.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, &result, http.StatusOK)
	return &result, err
}

func (d *dockerClient) remove(ctx context.Context, id string) error {
	return d.call(ctx, http.MethodDelete, "/containers/"+id+"?force=1", nil, nil, http.StatusNoContent)
}

type dockerLogReader struct {
	body      io.ReadCloser
	remaining uint32
}

func (r *dockerLogReader) Close() error { return r.body.Close() }

func (r *dockerLogReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for r.remaining == 0 {
		var header [8]byte
		if _, err := io.ReadFull(r.body, header[:]); err != nil {
			return 0, err
		}
		if header[0] != 1 && header[0] != 2 {
			return 0, fmt.Errorf("unsupported Docker log stream %d", header[0])
		}
		r.remaining = binary.BigEndian.Uint32(header[4:])
		if r.remaining > 4<<20 {
			return 0, fmt.Errorf("Docker log frame exceeds 4 MiB")
		}
	}
	if uint32(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.body.Read(p)
	r.remaining -= uint32(n)
	return n, err
}

func (d *dockerClient) logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error) {
	path := "/containers/" + url.PathEscape(id) + "/logs?stdout=1&stderr=1&follow=0"
	if follow {
		path = "/containers/" + url.PathEscape(id) + "/logs?stdout=1&stderr=1&follow=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://macd"+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		var detail dockerError
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&detail)
		return nil, &dockerAPIError{Method: http.MethodGet, Path: path, Status: response.StatusCode, Message: detail.Message}
	}
	return &dockerLogReader{body: response.Body}, nil
}
