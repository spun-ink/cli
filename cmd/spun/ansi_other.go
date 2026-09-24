//go:build !windows

package main

import "os"

// ansiColour: every Unix terminal takes the escape codes Bobbin is drawn with.
func ansiColour(*os.File) bool { return true }
