package main

import (
	"reflect"
	"testing"

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

func TestLastLogLines(t *testing.T) {
	for _, tt := range []struct {
		name, input, want string
		count             int
	}{
		{name: "one", input: "first\nsecond\nthird\n", count: 1, want: "third\n"},
		{name: "two", input: "first\nsecond\nthird\n", count: 2, want: "second\nthird\n"},
		{name: "more than available", input: "first\nsecond\n", count: 4, want: "first\nsecond\n"},
		{name: "no final newline", input: "first\nsecond", count: 1, want: "second"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(lastLogLines([]byte(tt.input), tt.count)); got != tt.want {
				t.Fatalf("lastLogLines = %q, want %q", got, tt.want)
			}
		})
	}
}
