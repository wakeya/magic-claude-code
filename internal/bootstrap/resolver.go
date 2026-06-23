package bootstrap

import (
	"fmt"

	"magic-claude-code/internal/i18n"
)

func normalizeMode(mode Mode) Mode {
	switch mode {
	case ModeTunnel:
		return ModeTunnel
	case ModeGateway:
		return ModeGateway
	default:
		return ModeTransparent
	}
}

// resolveMode selects the best connection mode based on the requested mode,
// capabilities and step results. Transparent can fall back to Tunnel/Gateway;
// manually selected Tunnel/Gateway stay fixed.
func resolveMode(preferred Mode, caps Capabilities, hosts, trust StepResult) (Mode, string) {
	switch normalizeMode(preferred) {
	case ModeTunnel:
		return ModeTunnel, ""
	case ModeGateway:
		return ModeGateway, ""
	}

	if caps.IsDocker && !caps.HasHostHelper {
		return ModeTunnel, reasonDockerTunnel(caps)
	}

	if hosts.Success && trust.Success {
		return ModeTransparent, ""
	}
	if !caps.CanEditHosts && !caps.CanTrustCA {
		return ModeGateway, reasonGateway(caps)
	}
	if !hosts.Success {
		return ModeTunnel, reasonTunnelFromHostsFailure(caps, hosts, trust)
	}
	if !trust.Success {
		return ModeTunnel, reasonTunnelFromTrustFailure(caps, trust)
	}
	return ModeTunnel, reasonTunnelGeneric(caps)
}

func resolveModeLocalized(preferred Mode, caps Capabilities, hosts, trust StepResult, locale string) (Mode, string) {
	msg := i18n.Load(locale)

	switch normalizeMode(preferred) {
	case ModeTunnel:
		return ModeTunnel, ""
	case ModeGateway:
		return ModeGateway, ""
	}

	if caps.IsDocker && !caps.HasHostHelper {
		return ModeTunnel, msg.BootstrapReasonDockerTunnel
	}

	if hosts.Success && trust.Success {
		return ModeTransparent, ""
	}
	if !caps.CanEditHosts && !caps.CanTrustCA {
		return ModeGateway, msg.BootstrapReasonGateway
	}
	if !hosts.Success {
		if hosts.Err != nil {
			return ModeTunnel, fmt.Sprintf(msg.BootstrapReasonHostsFailure, hosts.Err)
		}
		return ModeTunnel, msg.BootstrapReasonTunnelGeneric
	}
	if !trust.Success {
		if trust.Err != nil {
			return ModeTunnel, fmt.Sprintf(msg.BootstrapReasonTrustFailure, trust.Err)
		}
		return ModeTunnel, msg.BootstrapReasonTunnelGeneric
	}
	return ModeTunnel, msg.BootstrapReasonTunnelGeneric
}

func reasonTunnelFromHostsFailure(caps Capabilities, hosts, trust StepResult) string {
	if hosts.Err != nil {
		return fmt.Sprintf("hosts modification failed (%v); falling back to Tunnel Mode", hosts.Err)
	}
	return "hosts modification unavailable; Tunnel Mode can still intercept via HTTPS_PROXY"
}

func reasonTunnelFromTrustFailure(caps Capabilities, trust StepResult) string {
	if trust.Err != nil {
		return fmt.Sprintf("CA trust installation failed (%v); Tunnel Mode still usable with runtime CA trust", trust.Err)
	}
	return "CA trust unavailable; Tunnel Mode still usable with runtime CA trust"
}

func reasonDockerTunnel(caps Capabilities) string {
	return "Docker container cannot modify host; Tunnel Mode is the best available fallback"
}

func reasonGateway(caps Capabilities) string {
	if caps.IsDocker {
		return "Docker without helper and without proxy support; Gateway Mode is the only remaining option"
	}
	return "neither hosts nor CA trust available; Gateway Mode is the only remaining option"
}

func reasonTunnelGeneric(caps Capabilities) string {
	return "Transparent Mode is incomplete; Tunnel Mode is the next available fallback"
}
