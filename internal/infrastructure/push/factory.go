package push

// New selects the provider implementation from its configured name and
// configuration. Anything other than "fcm" resolves to the no-op — safer
// than accidentally hitting a real Firebase project through a misconfigured
// worker.
func New(name string, cfg FCMConfig) (Provider, error) {
	if name == "fcm" {
		return NewFCMFromBytes(cfg)
	}
	return &NoopProvider{}, nil
}
