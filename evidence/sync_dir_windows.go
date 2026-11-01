//go:build windows

package evidence

// Windows does not expose a portable directory fsync equivalent. The file body
// was flushed before the rename; NTFS journaling and read-time hash verification
// cover this weaker metadata-durability window.
func syncDir(string) error { return nil }
