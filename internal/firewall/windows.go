//go:build windows

package firewall

import "context"

// inspectPlatform delega en la detección de Windows.
func inspectPlatform(ctx context.Context, opts Options) Plan {
	return inspectWindows(ctx, opts)
}
