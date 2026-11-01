//go:build !windows

package server

// stdioLaunch leaves native Unix executables untouched.
func stdioLaunch(command string, args []string) (string, []string, error) {
	return command, args, nil
}
