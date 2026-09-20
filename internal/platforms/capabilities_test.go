package platforms

import "testing"

func TestCapabilities(t *testing.T) {
	caps, err := Capabilities()
	if err != nil {
		t.Fatalf("Capabilities() error: %v", err)
	}
	for _, key := range []string{"meta", "tiktok", "google", "linkedinads", "pinterestads", "xads", "openaiads"} {
		c, ok := caps.Capabilities[key]
		if !ok {
			t.Errorf("missing platform %q", key)
			continue
		}
		if !c.Available {
			t.Errorf("platform %q should be available", key)
		}
	}
	if c := caps.Capabilities["linkedin"]; c.Available {
		t.Error("linkedin should be marked unavailable until its adapter ships")
	}
}
