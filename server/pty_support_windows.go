//go:build windows

package server

// Norma's Windows PTY implementation is an explicit "not supported" stub.
// Do not advertise shell_* tools that can never succeed there.
const ptySupported = false
