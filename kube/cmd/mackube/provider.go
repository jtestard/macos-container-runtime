package main

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	statsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

type managedPod struct {
	pod         *corev1.Pod
	containerID string
}

type provider struct {
	docker     *dockerClient
	mu         sync.Mutex
	pods       map[string]*managedPod
	notify     func(*corev1.Pod)
	node       *corev1.Node
	nodeNotify func(*corev1.Node)
	healthy    bool
}

func newProvider(d *dockerClient) *provider {
	return &provider{docker: d, pods: make(map[string]*managedPod), healthy: true}
}

func key(namespace, name string) string    { return namespace + "/" + name }
func containerName(pod *corev1.Pod) string { return "kube-" + string(pod.UID) }

func validatePod(pod *corev1.Pod) error {
	if len(pod.Spec.Containers) != 1 || len(pod.Spec.InitContainers) != 0 || len(pod.Spec.EphemeralContainers) != 0 {
		return errdefs.InvalidInput("macnative requires exactly one regular container")
	}
	if !pod.Spec.HostNetwork {
		return errdefs.InvalidInput("macnative requires hostNetwork: true")
	}
	if len(pod.Spec.Volumes) != 0 || len(pod.Spec.ImagePullSecrets) != 0 {
		return errdefs.InvalidInput("volumes and image pull secrets are unsupported")
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		return errdefs.InvalidInput("set automountServiceAccountToken: false")
	}
	if pod.Spec.EnableServiceLinks == nil || *pod.Spec.EnableServiceLinks {
		return errdefs.InvalidInput("set enableServiceLinks: false")
	}
	if (pod.Spec.SecurityContext != nil && !reflect.DeepEqual(*pod.Spec.SecurityContext, corev1.PodSecurityContext{})) || len(pod.Spec.HostAliases) != 0 {
		return errdefs.InvalidInput("Pod security context and host aliases are unsupported")
	}
	c := pod.Spec.Containers[0]
	if c.Image == "" || len(c.Command) != 0 || len(c.Args) != 0 || len(c.Env) != 0 || len(c.EnvFrom) != 0 || len(c.VolumeMounts) != 0 || len(c.VolumeDevices) != 0 || c.SecurityContext != nil || c.Stdin || c.TTY {
		return errdefs.InvalidInput("container overrides, environment, mounts, security context, stdin, and TTY are unsupported")
	}
	if c.ImagePullPolicy == corev1.PullAlways {
		return errdefs.InvalidInput("imagePullPolicy Always is unsupported; use Never or IfNotPresent")
	}
	if c.LivenessProbe != nil || c.ReadinessProbe != nil || c.StartupProbe != nil {
		return errdefs.InvalidInput("Kubernetes probes are unsupported by this provider")
	}
	return nil
}

func (p *provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	if err := validatePod(pod); err != nil {
		return err
	}
	k := key(pod.Namespace, pod.Name)
	p.mu.Lock()
	if existing := p.pods[k]; existing != nil {
		p.mu.Unlock()
		return nil
	}
	for oldKey, old := range p.pods {
		state, err := p.docker.inspect(ctx, old.containerID)
		if err != nil && !dockerNotFound(err) {
			p.mu.Unlock()
			return err
		}
		if err == nil && state.State.Running {
			p.mu.Unlock()
			return fmt.Errorf("macnative currently has capacity for one Pod")
		}
		// A terminal Pod has already been reported to Kubernetes. Its
		// process no longer occupies the native slot; clear its macd record
		// before accepting the ReplicaSet's replacement Pod.
		_ = p.docker.remove(ctx, old.containerID)
		delete(p.pods, oldKey)
	}
	// Hold the lock across create/start so concurrent controller workers cannot
	// launch a second host-network process.
	defer p.mu.Unlock()
	image := pod.Spec.Containers[0].Image
	if err := p.docker.imageExists(ctx, image); err != nil {
		return err
	}
	// The adapter keeps its map in memory, while macd may outlive it. A Pod
	// already launched before adapter restart has the same UID-derived name;
	// adopt its running process instead of starting a second one.
	name := containerName(pod)
	if existing, err := p.docker.inspect(ctx, name); err == nil {
		if existing.State.Status == "created" {
			if err := p.docker.start(ctx, existing.ID); err != nil {
				return err
			}
			existing, err = p.docker.inspect(ctx, name)
			if err != nil {
				return err
			}
		}
		copy := pod.DeepCopy()
		copy.Status = statusFor(copy, existing)
		p.pods[k] = &managedPod{pod: copy, containerID: existing.ID}
		if cb := p.notify; cb != nil {
			go cb(copy.DeepCopy())
		}
		return nil
	} else if !dockerNotFound(err) {
		return err
	}
	if n, err := p.docker.runningCount(ctx); err != nil {
		return err
	} else if n != 0 {
		return fmt.Errorf("macd already has a running container; stop it before scheduling a Pod")
	}
	id, err := p.docker.create(ctx, image, name)
	if err != nil {
		return err
	}
	if err := p.docker.start(ctx, id); err != nil {
		_ = p.docker.remove(ctx, id)
		return err
	}
	copy := pod.DeepCopy()
	copy.Status = statusFor(copy, &dockerInspect{ID: id, Image: image})
	p.pods[k] = &managedPod{pod: copy, containerID: id}
	go p.refreshOne(context.Background(), k)
	return nil
}

func (p *provider) UpdatePod(_ context.Context, pod *corev1.Pod) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if managed := p.pods[key(pod.Namespace, pod.Name)]; managed != nil {
		copy := pod.DeepCopy()
		copy.Status = *managed.pod.Status.DeepCopy()
		managed.pod = copy
	}
	return nil
}

func (p *provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	k := key(pod.Namespace, pod.Name)
	p.mu.Lock()
	managed := p.pods[k]
	p.mu.Unlock()
	if managed == nil {
		return nil
	}
	if err := p.docker.remove(ctx, managed.containerID); err != nil && !dockerNotFound(err) {
		return err
	}
	p.mu.Lock()
	if current := p.pods[k]; current == managed {
		copy := managed.pod.DeepCopy()
		copy.Status.Phase = corev1.PodSucceeded
		copy.Status.Reason = "Deleted"
		for i := range copy.Status.ContainerStatuses {
			copy.Status.ContainerStatuses[i].Ready = false
			copy.Status.ContainerStatuses[i].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Deleted", FinishedAt: metav1.Now()}}
		}
		delete(p.pods, k)
		cb := p.notify
		p.mu.Unlock()
		if cb != nil {
			cb(copy)
		}
		return nil
	}
	p.mu.Unlock()
	return nil
}

func (p *provider) GetPod(_ context.Context, namespace, name string) (*corev1.Pod, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if managed := p.pods[key(namespace, name)]; managed != nil {
		return managed.pod.DeepCopy(), nil
	}
	return nil, errdefs.NotFoundf("Pod %s/%s is not known", namespace, name)
}

func (p *provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	return pod.Status.DeepCopy(), nil
}

func (p *provider) GetPods(_ context.Context) ([]*corev1.Pod, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]*corev1.Pod, 0, len(p.pods))
	for _, managed := range p.pods {
		result = append(result, managed.pod.DeepCopy())
	}
	return result, nil
}

func (p *provider) NotifyPods(_ context.Context, cb func(*corev1.Pod)) {
	p.mu.Lock()
	p.notify = cb
	p.mu.Unlock()
}

func (p *provider) Ping(ctx context.Context) error {
	err := p.docker.ping(ctx)
	p.mu.Lock()
	healthy := err == nil
	if healthy != p.healthy && p.node != nil {
		p.healthy = healthy
		updated := p.node.DeepCopy()
		for i := range updated.Status.Conditions {
			if updated.Status.Conditions[i].Type == corev1.NodeReady {
				condition := &updated.Status.Conditions[i]
				condition.Status = corev1.ConditionFalse
				condition.Reason = "MacdUnavailable"
				condition.Message = "macd socket is unavailable"
				if healthy {
					condition.Status = corev1.ConditionTrue
					condition.Reason = "MacdReady"
					condition.Message = "macd socket is reachable"
				}
				condition.LastTransitionTime = metav1.Now()
				break
			}
		}
		p.node = updated
		cb := p.nodeNotify
		p.mu.Unlock()
		if cb != nil {
			go cb(updated)
		}
		return nil
	}
	p.mu.Unlock()
	// Health is reported through NodeReady so the node controller can still
	// publish the unhealthy state when macd is down.
	return nil
}

func (p *provider) NotifyNodeStatus(_ context.Context, cb func(*corev1.Node)) {
	p.mu.Lock()
	p.nodeNotify = cb
	p.mu.Unlock()
}

func (p *provider) poll(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			keys := make([]string, 0, len(p.pods))
			for k := range p.pods {
				keys = append(keys, k)
			}
			p.mu.Unlock()
			for _, k := range keys {
				p.refreshOne(ctx, k)
			}
		}
	}
}

func (p *provider) refreshOne(ctx context.Context, k string) {
	p.mu.Lock()
	managed := p.pods[k]
	p.mu.Unlock()
	if managed == nil {
		return
	}
	state, err := p.docker.inspect(ctx, managed.containerID)
	if err != nil {
		return
	}
	p.mu.Lock()
	if p.pods[k] != managed {
		p.mu.Unlock()
		return
	}
	updated := managed.pod.DeepCopy()
	updated.Status = statusFor(updated, state)
	if !sameStatus(&managed.pod.Status, &updated.Status) {
		managed.pod = updated
		cb := p.notify
		p.mu.Unlock()
		if cb != nil {
			cb(updated.DeepCopy())
		}
		return
	}
	p.mu.Unlock()
}

func sameStatus(a, b *corev1.PodStatus) bool {
	return a.Phase == b.Phase && len(a.ContainerStatuses) == len(b.ContainerStatuses) &&
		(len(a.ContainerStatuses) == 0 || (a.ContainerStatuses[0].Ready == b.ContainerStatuses[0].Ready &&
			a.ContainerStatuses[0].State.Running != nil == (b.ContainerStatuses[0].State.Running != nil) &&
			a.ContainerStatuses[0].State.Terminated != nil == (b.ContainerStatuses[0].State.Terminated != nil)))
}

func statusFor(pod *corev1.Pod, state *dockerInspect) corev1.PodStatus {
	c := pod.Spec.Containers[0]
	status := corev1.PodStatus{Phase: corev1.PodPending, HostIP: "", ContainerStatuses: []corev1.ContainerStatus{{
		Name: c.Name, Image: c.Image, ImageID: state.Image, ContainerID: "macnative://" + state.ID,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "Starting"}},
	}}}
	cs := &status.ContainerStatuses[0]
	switch state.State.Status {
	case "running":
		status.Phase = corev1.PodRunning
		cs.Ready = true
		cs.Started = new(bool)
		*cs.Started = true
		cs.State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(state.State.StartedAt)}}
		status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}, {Type: corev1.ContainersReady, Status: corev1.ConditionTrue}}
	case "exited":
		status.Phase = corev1.PodSucceeded
		reason := "Completed"
		if state.State.ExitCode != 0 {
			status.Phase, reason = corev1.PodFailed, "Error"
		}
		cs.State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: int32(state.State.ExitCode), Reason: reason, StartedAt: metav1.NewTime(state.State.StartedAt), FinishedAt: metav1.Now()}}
	}
	return status
}

func unsupported() error { return fmt.Errorf("macnative does not support this kubelet operation yet") }
func (p *provider) GetContainerLogs(context.Context, string, string, string, api.ContainerLogOpts) (io.ReadCloser, error) {
	return nil, unsupported()
}
func (p *provider) RunInContainer(context.Context, string, string, string, []string, api.AttachIO) error {
	return unsupported()
}
func (p *provider) AttachToContainer(context.Context, string, string, string, api.AttachIO) error {
	return unsupported()
}
func (p *provider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	return nil, unsupported()
}
func (p *provider) GetMetricsResource(context.Context) ([]*dto.MetricFamily, error) {
	return nil, unsupported()
}
func (p *provider) PortForward(context.Context, string, string, int32, io.ReadWriteCloser) error {
	return unsupported()
}
