package main

import (
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
