//go:build !windows

package main

// fileFallbackAllowed: an owner-only 0600 file is private here.
const fileFallbackAllowed = true
