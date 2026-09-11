package payments

// New selects the provider implementation from its configured name. Anything
// other than "chapa" resolves to the no-op — safer than accidentally charging
// real money through a misconfigured gateway.
func New(name string, baseURL, secretKey string) Provider {
	if name == chapaName {
		return NewChapa(baseURL, secretKey)
	}
	return &NoopProvider{}
}
