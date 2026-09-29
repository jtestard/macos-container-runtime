package main

import (
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestValidateKubeconfig(t *testing.T) {
	tests := []struct {
		name        string
		current     string
		contexts    []string
		kindCluster string
		wantError   bool
	}{
		{name: "non Kind context", current: "production", contexts: []string{"production"}},
		{name: "Kind context without guard", current: "kind-livekit", contexts: []string{"kind-livekit"}},
		{name: "Kind context with guard", current: "kind-livekit", contexts: []string{"kind-livekit"}, kindCluster: "livekit"},
		{name: "wrong Kind context", current: "kind-livekit", contexts: []string{"kind-livekit"}, kindCluster: "other", wantError: true},
		{name: "multiple contexts", current: "production", contexts: []string{"production", "staging"}, wantError: true},
		{name: "missing selection", contexts: []string{"production"}, wantError: true},
		{name: "unknown selection", current: "missing", contexts: []string{"production"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := clientcmdapi.NewConfig()
			config.CurrentContext = tt.current
			for _, name := range tt.contexts {
				config.Contexts[name] = clientcmdapi.NewContext()
			}
			err := validateKubeconfig(config, tt.kindCluster)
			if (err != nil) != tt.wantError {
				t.Fatalf("validateKubeconfig() error = %v, want error %v", err, tt.wantError)
			}
		})
	}
}
