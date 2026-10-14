package notes

import (
	"time"

	"golang.org/x/sys/unix"
)

func birthTime(real string, fallback time.Time) time.Time {
	var st unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, real, 0, unix.STATX_BTIME, &st); err == nil && st.Mask&unix.STATX_BTIME != 0 {
		return time.Unix(st.Btime.Sec, int64(st.Btime.Nsec))
	}
	return fallback
}
