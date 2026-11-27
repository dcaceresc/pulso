//go:build !linux && !windows

package firewall

import "context"

// inspectPlatform explica que este sistema no está cubierto por el diagnóstico.
func inspectPlatform(_ context.Context, _ Options) Plan {
	return Plan{
		Backend:  BackendUnknown,
		Findings: []Finding{{Kind: Unverifiable, Backend: BackendUnknown}},
	}
}
