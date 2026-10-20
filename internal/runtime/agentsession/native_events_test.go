package agentsession

func (r nativeEventRegistry) entries() []nativeEventBinding {
	entries := make([]nativeEventBinding, len(r.bindings))
	copy(entries, r.bindings)
	return entries
}
