package mcpgateway

func (d *sdkDownstream) routeProgress(frame []byte) {
	parsed, err := parseFrameJSON(frame)
	if err != nil {
		return
	}
	d.routeProgressFrame(parsed)
}
