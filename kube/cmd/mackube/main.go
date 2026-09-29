package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const defaultNodeName = "macnative"
const runtimeLabel = "macnative.dev/runtime"
const runtimeValue = "darwin-arm64"
const slotLabel = "macnative.dev/slot"

func main() {
	kubeconfig := flag.String("kubeconfig", "", "explicit kubeconfig path (defaults to KUBECONFIG)")
	kindCluster := flag.String("kind-cluster", "", "optional Kind cluster name to require a matching kind-<name> context")
	nodeName := flag.String("node-name", defaultNodeName, "virtual node name (one per macd instance)")
	slot := flag.String("slot", "", "optional workload label for Pod placement")
	socket := flag.String("socket", "/private/tmp/macnative-docker.sock", "macd Docker socket")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: mackube [-kubeconfig path] [-kind-cluster name] [-node-name name] [-slot name] [-socket path]")
		os.Exit(2)
	}
	if err := run(*kubeconfig, *kindCluster, *nodeName, *slot, *socket); err != nil {
		fmt.Fprintln(os.Stderr, "mackube:", err)
		os.Exit(1)
	}
}

func run(kubeconfig, kindCluster, nodeName, slot, socket string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	backend := newDockerClient(socket)
	if err := backend.ping(ctx); err != nil {
		return fmt.Errorf("macd unavailable: %w", err)
	}
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}
	if kubeconfig == "" {
		return fmt.Errorf("set -kubeconfig explicitly so this process cannot use an unintended cluster context")
	}
	config, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		return err
	}
	if err := validateKubeconfig(config, kindCluster); err != nil {
		return err
	}
	if nodeName == "" {
		return fmt.Errorf("node name must not be empty")
	}
	client, err := nodeutil.ClientsetFromEnv(kubeconfig)
	if err != nil {
		return err
	}
	provider := newProvider(backend)
	n, err := nodeutil.NewNode(nodeName, func(cfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		provider.node = cfg.Node.DeepCopy()
		return provider, provider, nil
	}, nodeutil.WithClient(client), func(cfg *nodeutil.NodeConfig) error {
		cfg.KubeconfigPath = kubeconfig
		cfg.NumWorkers = 1
		cfg.SkipDownwardAPIResolution = true
		cfg.NodeSpec.Labels[runtimeLabel] = runtimeValue
		if slot != "" {
			cfg.NodeSpec.Labels[slotLabel] = slot
		}
		cfg.NodeSpec.Labels[corev1.LabelOSStable] = "darwin"
		cfg.NodeSpec.Labels[corev1.LabelArchStable] = "arm64"
		cfg.NodeSpec.Spec.ProviderID = "macnative://" + nodeName
		cfg.NodeSpec.Spec.Taints = []corev1.Taint{{Key: runtimeLabel, Value: runtimeValue, Effect: corev1.TaintEffectNoSchedule}}
		cfg.NodeSpec.Status.Capacity = capacity()
		cfg.NodeSpec.Status.Allocatable = capacity()
		cfg.NodeSpec.Status.NodeInfo = corev1.NodeSystemInfo{Architecture: "arm64", OperatingSystem: "darwin", KubeletVersion: "macnative/0.1"}
		cfg.NodeSpec.Status.Phase = corev1.NodeRunning
		cfg.NodeSpec.Status.Conditions = []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue, Reason: "MacdReady", Message: "macd socket is reachable", LastHeartbeatTime: metav1.Now(), LastTransitionTime: metav1.Now()},
			{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse, Reason: "NoPressure"},
			{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse, Reason: "NoPressure"},
			{Type: corev1.NodePIDPressure, Status: corev1.ConditionFalse, Reason: "NoPressure"},
			{Type: corev1.NodeNetworkUnavailable, Status: corev1.ConditionFalse, Reason: "HostNetwork"},
		}
		return nil
	})
	if err != nil {
		return err
	}
	go provider.poll(ctx, 2*time.Second)
	return n.Run(ctx)
}

func validateKubeconfig(config *clientcmdapi.Config, kindCluster string) error {
	if len(config.Contexts) != 1 || config.CurrentContext == "" || config.Contexts[config.CurrentContext] == nil {
		return fmt.Errorf("kubeconfig must contain exactly one context and select it as current; current context is %q and context count is %d", config.CurrentContext, len(config.Contexts))
	}
	if kindCluster != "" && config.CurrentContext != "kind-"+kindCluster {
		return fmt.Errorf("-kind-cluster %q requires context %q, got %q", kindCluster, "kind-"+kindCluster, config.CurrentContext)
	}
	return nil
}

func capacity() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1"),
		corev1.ResourceMemory: resource.MustParse("1Gi"),
		corev1.ResourcePods:   resource.MustParse("1"),
	}
}
