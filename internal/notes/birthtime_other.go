//go:build !linux

package notes

import "time"

func birthTime(_ string, fallback time.Time) time.Time {
	return fallback
}
