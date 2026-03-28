package warp

import (
	"net/http"
	"strings"

	"orchids-api/internal/config"
)

const (
	warpTransportStandard = "standard"
	warpTransportUTLS     = "utls"
	warpTransportBrowser  = "browser"
)

type clientProfile struct {
	TransportProfile string
	UserAgent        string
	ClientID         string
	ClientVersion    string
	OSCategory       string
	OSName           string
	OSVersion        string
}

func clientProfileFromConfig(cfg *config.Config) clientProfile {
	profile := clientProfile{
		TransportProfile: warpTransportStandard,
		ClientID:         clientID,
		ClientVersion:    clientVersion,
		OSCategory:       clientOSCategory,
		OSName:           clientOSName,
		OSVersion:        clientOSVersion,
	}
	if cfg == nil {
		return profile
	}

	switch strings.ToLower(strings.TrimSpace(cfg.WarpTransportProfile)) {
	case "", warpTransportStandard:
		profile.TransportProfile = warpTransportStandard
	case warpTransportUTLS:
		profile.TransportProfile = warpTransportUTLS
	case warpTransportBrowser:
		profile.TransportProfile = warpTransportBrowser
	default:
		profile.TransportProfile = warpTransportStandard
	}
	if cfg.WarpUseUTLS && strings.TrimSpace(cfg.WarpTransportProfile) == "" {
		profile.TransportProfile = warpTransportUTLS
	}

	if value := strings.TrimSpace(cfg.WarpClientOSCategory); value != "" {
		profile.OSCategory = value
	}
	if value := strings.TrimSpace(cfg.WarpClientOSName); value != "" {
		profile.OSName = value
	}
	if value := strings.TrimSpace(cfg.WarpClientOSVersion); value != "" {
		profile.OSVersion = value
	}
	if value := strings.TrimSpace(cfg.WarpUserAgent); value != "" {
		profile.UserAgent = value
	}
	return profile
}

func (p clientProfile) applyWarpHeaders(headers http.Header) {
	headers.Set("X-Warp-Client-ID", p.ClientID)
	headers.Set("X-Warp-Client-Version", p.ClientVersion)
	headers.Set("X-Warp-OS-Category", p.OSCategory)
	headers.Set("X-Warp-OS-Name", p.OSName)
	headers.Set("X-Warp-OS-Version", p.OSVersion)
}

func (p clientProfile) applyUserAgent(headers http.Header) {
	if strings.TrimSpace(p.UserAgent) == "" {
		return
	}
	headers.Set("User-Agent", p.UserAgent)
}

func (p clientProfile) graphQLUserAgent() string {
	if strings.TrimSpace(p.UserAgent) != "" {
		return p.UserAgent
	}
	return userAgent
}

func (p clientProfile) requestContextPayload() map[string]interface{} {
	return map[string]interface{}{
		"clientContext": map[string]interface{}{
			"version": p.ClientVersion,
		},
		"osContext": map[string]interface{}{
			"category":           p.OSCategory,
			"linuxKernelVersion": nil,
			"name":               p.OSName,
			"version":            p.OSVersion,
		},
	}
}
