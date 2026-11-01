//go:build !windows

package evidence

import "os"

// syncDir makes the rename durable on Unix filesystems. Windows has no
// equivalent directory fsync, so the Windows implementation is a no-op.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
