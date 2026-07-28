package platforms

import (
	"encoding/json"
	"sync"
)

// PlatformCapabilities is the per-platform capability matrix. Each value is
// "supported", "partial", "unsupported", or "deprecated".
type PlatformCapabilities struct {
	Targeting       map[string]string            `json:"targeting"`
	Bidding         map[string]string            `json:"bidding"`
	FrequencyCap    string                       `json:"frequency_cap"`
	Pacing          map[string]string            `json:"pacing"`
	Creative        map[string]string            `json:"creative"`
	Objectives      map[string]string            `json:"objectives"`
	Forecast        string                       `json:"forecast"`
}

type CapabilitiesResponse struct {
	Capabilities map[string]PlatformCapabilities `json:"capabilities"`
}

var (
	capabilities     *CapabilitiesResponse
	capabilitiesOnce sync.Once
)

func Capabilities() (*CapabilitiesResponse, error) {
	var initErr error
	capabilitiesOnce.Do(func() {
		var parsed CapabilitiesResponse
		if err := json.Unmarshal(rawCapabilities, &parsed.Capabilities); err != nil {
			initErr = err
			return
		}
		capabilities = &parsed
	})
	if initErr != nil {
		return nil, initErr
	}
	return capabilities, nil
}
