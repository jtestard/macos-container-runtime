package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	corev1 "k8s.io/api/core/v1"
)

func TestValidatePodAcceptsKubernetesEmptyDefaults(t *testing.T) {
	no := false
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		HostNetwork:                  true,
		AutomountServiceAccountToken: &no,
		EnableServiceLinks:           &no,
		SecurityContext:              &corev1.PodSecurityContext{},
		Containers:                   []corev1.Container{{Name: "server", Image: "kube-web:latest", ImagePullPolicy: corev1.PullNever}},
	}}
	if err := validatePod(pod); err != nil {
		t.Fatalf("defaulted Pod rejected: %v", err)
	}
	pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "UNSUPPORTED", Value: "1"}}
	if err := validatePod(pod); err == nil {
		t.Fatal("environment override was accepted")
	}
}

func TestGetContainerLogsAcceptsTailWithFollow(t *testing.T) {
	before := time.Now().Add(-48 * time.Hour).Unix()
	docker := &dockerClient{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("tail") != "10" || r.URL.Query().Get("follow") != "1" || r.URL.Query().Get("timestamps") != "1" {
			t.Errorf("unexpected log request: %s", r.URL.String())
		}
		if got := r.URL.Query().Get("since"); got != fmt.Sprint(before) && got != fmt.Sprint(before+1) {
			t.Errorf("unexpected since timestamp: %q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	p := newProvider(docker)
	p.pods[key("livekit", "kokoro")] = &managedPod{
		pod:         &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "kokoro"}}}},
		containerID: "container-id",
	}
	logs, err := p.GetContainerLogs(context.Background(), "livekit", "kokoro", "kokoro", api.ContainerLogOpts{Tail: 10, Follow: true, SinceSeconds: 48 * 60 * 60, Timestamps: true})
	if err != nil {
		t.Fatal(err)
	}
	logs.Close()
}

func TestValidatePodReadOnlyHostPathAndArgs(t *testing.T) {
	no := false
	directory := corev1.HostPathDirectory
	path := t.TempDir()
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		HostNetwork:                  true,
		AutomountServiceAccountToken: &no,
		EnableServiceLinks:           &no,
		Volumes: []corev1.Volume{{Name: "model", VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: path, Type: &directory},
		}}},
		Containers: []corev1.Container{{
			Name: "server", Image: "llama-server:local", ImagePullPolicy: corev1.PullNever,
			Args:         []string{"-m", "models/model.gguf", "--port", "8080"},
			VolumeMounts: []corev1.VolumeMount{{Name: "model", MountPath: "/app/models", ReadOnly: true}},
		}},
	}}
	if err := validatePod(pod); err != nil {
		t.Fatalf("read-only model mount rejected: %v", err)
	}
	binds, err := podBinds(pod)
	if err != nil || !reflect.DeepEqual(binds, []string{path + ":/app/models:ro"}) {
		t.Fatalf("podBinds = %v, %v", binds, err)
	}
	for name, change := range map[string]func(*corev1.Pod){
		"writable": func(p *corev1.Pod) { p.Spec.Containers[0].VolumeMounts[0].ReadOnly = false },
		"subPath":  func(p *corev1.Pod) { p.Spec.Containers[0].VolumeMounts[0].SubPath = "model.gguf" },
		"wrong type": func(p *corev1.Pod) {
			file := corev1.HostPathFile
			p.Spec.Volumes[0].HostPath.Type = &file
		},
		"missing mount": func(p *corev1.Pod) { p.Spec.Containers[0].VolumeMounts = nil },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := pod.DeepCopy()
			change(invalid)
			if err := validatePod(invalid); err == nil {
				t.Fatal("unsupported volume configuration accepted")
			}
		})
	}
}

func TestRefreshMarksPodFailedWhenMacdForgetsContainer(t *testing.T) {
	reachable := true
	docker := &dockerClient{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !reachable {
			return nil, fmt.Errorf("connection refused")
		}
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"message":"No such container: container-id"}`))}, nil
	})}}
	p := newProvider(docker)
	running := corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
		Name: "kokoro", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}}
	k := key("livekit", "kokoro")
	p.pods[k] = &managedPod{
		pod:         &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "kokoro"}}}, Status: running},
		containerID: "container-id",
	}
	var notified []*corev1.Pod
	p.notify = func(pod *corev1.Pod) { notified = append(notified, pod) }

	// An unreachable macd says nothing about the container: keep the status.
	reachable = false
	p.refreshOne(context.Background(), k)
	if len(notified) != 0 || p.pods[k].pod.Status.Phase != corev1.PodRunning {
		t.Fatalf("status changed while macd was unreachable: %+v", p.pods[k].pod.Status)
	}

	reachable = true
	p.refreshOne(context.Background(), k)
	if len(notified) != 1 {
		t.Fatalf("got %d notifications, want 1", len(notified))
	}
	got := notified[0].Status
	if got.Phase != corev1.PodFailed || got.Reason != "ContainerMissing" {
		t.Fatalf("got phase %q reason %q, want Failed ContainerMissing", got.Phase, got.Reason)
	}
	if cs := got.ContainerStatuses[0]; cs.Ready || cs.State.Terminated == nil {
		t.Fatalf("container status should be terminated and not ready: %+v", cs)
	}

	// Later polls must not report the same failure again.
	p.refreshOne(context.Background(), k)
	if len(notified) != 1 {
		t.Fatalf("got %d notifications after a repeat poll, want 1", len(notified))
	}
}
