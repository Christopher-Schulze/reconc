package agentsession

type nativeEventBinding struct {
	route        string
	primary      string
	alternate    string
	allowMissing bool
}

type nativeEventRegistry struct {
	bindings []nativeEventBinding
}

func newNativeEventRegistry(bindings ...nativeEventBinding) nativeEventRegistry {
	owned := make([]nativeEventBinding, len(bindings))
	copy(owned, bindings)
	return nativeEventRegistry{bindings: owned}
}

func (r nativeEventRegistry) lookup(route string) (nativeEventBinding, bool) {
	for _, binding := range r.bindings {
		if binding.route == route {
			return binding, true
		}
	}
	return nativeEventBinding{}, false
}

func (r nativeEventRegistry) entries() []nativeEventBinding {
	entries := make([]nativeEventBinding, len(r.bindings))
	copy(entries, r.bindings)
	return entries
}
