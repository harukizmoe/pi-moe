package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	appconfig "harukizmoe/pimoe/internal/config"
)

const (
	realProviderConfigEnv = "PIMOE_REAL_PROVIDER_CONFIG"
	realProviderNameEnv   = "PIMOE_REAL_PROVIDER_NAME"
)

// TestRealProviderRuntimeSmoke is an explicitly opt-in connectivity check. It is
// excluded from deterministic acceptance unless a real-provider config is set.
func TestRealProviderRuntimeSmoke(t *testing.T) {
	configPath := strings.TrimSpace(os.Getenv(realProviderConfigEnv))
	if configPath == "" {
		t.Skipf("set %s to run the optional real-provider smoke test", realProviderConfigEnv)
	}
	providerName := strings.TrimSpace(os.Getenv(realProviderNameEnv))
	loaded, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatalf("load real-provider config: %v", err)
	}
	if providerName == "" {
		providerName = loaded.LLMs.DefaultProvider
	}
	providerConfig, ok := loaded.LLMs.Providers[providerName]
	if !ok {
		t.Fatalf("real-provider config has no provider %q", providerName)
	}
	if providerConfig.Type == "fake" {
		t.Fatalf("provider %q is fake; optional smoke test requires a real provider", providerName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	runtime, err := NewConfiguredRuntime(ctx, Config{
		ProviderConfigPath: configPath,
		ProviderName:       providerName,
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("create real-provider Runtime: %v", err)
	}

	var answer string
	var terminal Event
	for event := range runtime.Run(ctx, NewRunRequest([]Message{UserMessage{Content: "Reply briefly to confirm connectivity."}})) {
		switch event := event.(type) {
		case MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				answer = event.Message.Content
			}
		case RunCompletedEvent, RunFailedEvent, RunCanceledEvent:
			terminal = event
		}
	}
	if _, ok := terminal.(RunCompletedEvent); !ok {
		t.Fatalf("real-provider terminal = %#v, want RunCompletedEvent", terminal)
	}
	if strings.TrimSpace(answer) == "" {
		t.Fatal("real-provider completed without a nonempty assistant response")
	}
}
