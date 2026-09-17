package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{
		"ANTREQUEUE_ADDR", "ANTREQUEUE_SERVICE_SECRET", "ANTREQUEUE_DISPATCH_RATE",
	} {
		t.Setenv(k, "")
	}

	cfg := Load()
	if cfg.Addr != ":8090" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":8090")
	}
	if cfg.ServiceSecret != "" {
		t.Errorf("ServiceSecret = %q, want empty so main can refuse to start", cfg.ServiceSecret)
	}
	if cfg.DispatchRate != 50 {
		t.Errorf("DispatchRate = %d, want 50", cfg.DispatchRate)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("ANTREQUEUE_ADDR", ":9999")
	t.Setenv("ANTREQUEUE_SERVICE_SECRET", "s3cret")
	t.Setenv("ANTREQUEUE_DISPATCH_RATE", "250")

	cfg := Load()
	if cfg.Addr != ":9999" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":9999")
	}
	if cfg.ServiceSecret != "s3cret" {
		t.Errorf("ServiceSecret = %q, want %q", cfg.ServiceSecret, "s3cret")
	}
	if cfg.DispatchRate != 250 {
		t.Errorf("DispatchRate = %d, want 250", cfg.DispatchRate)
	}
}

// A non-numeric rate must fall back to the default rather than dropping to 0,
// which would stall dispatch entirely.
func TestLoadIgnoresUnparseableRate(t *testing.T) {
	t.Setenv("ANTREQUEUE_DISPATCH_RATE", "fast")

	if got := Load().DispatchRate; got != 50 {
		t.Errorf("DispatchRate = %d, want the default 50", got)
	}
}
