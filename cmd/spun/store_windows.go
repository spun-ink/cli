//go:build windows

package main

// fileFallbackAllowed: 0600 is no ACL on Windows, and Credential Manager is always there.
const fileFallbackAllowed = false
