package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBobbinUsesOnlyPaletteColours(t *testing.T) {
	colors := bobbinPalette()
	if len(colors) != 7 {
		t.Fatalf("palette has %d colours", len(colors))
	}
	for _, scene := range []string{"digging", "shipping"} {
		for _, row := range sceneGrid(scene) {
			for i := range row {
				if _, ok := colors[row[i]]; !ok && row[i] != '.' {
					t.Fatalf("%s: %q is not in the palette", scene, row[i])
				}
			}
		}
	}
}

func TestBobbinIsCroppedToTheFigure(t *testing.T) {
	grid := sceneGrid("digging")
	if strings.Trim(grid[0], ".") == "" || strings.Trim(grid[len(grid)-1], ".") == "" {
		t.Fatal("empty margin rows survived the crop")
	}
	if lines := drawBobbin("digging"); len(lines) > 16 {
		t.Fatalf("Bobbin is %d rows tall", len(lines))
	}
}

func TestBobbinOnlyAtATerminal(t *testing.T) {
	isolate(t)
	for _, tty := range []bool{false, true} {
		var out bytes.Buffer
		a := &app{stdin: stdinWith(t, ""), stdout: &out, bobbin: tty}
		if _, err := a.run(nil); err != nil {
			t.Fatal(err)
		}
		if drawn := strings.Contains(out.String(), "▀"); drawn != tty {
			t.Fatalf("tty %v: Bobbin drawn %v", tty, drawn)
		}
		if !strings.Contains(out.String(), "Usage:") {
			t.Fatalf("tty %v: help missing", tty)
		}
	}
}

func TestLoginAtATerminalShowsBobbinInsteadOfJSON(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	var out bytes.Buffer
	a := &app{stdin: stdinWith(t, "good\n"), stdout: &out, bobbin: true}
	value, err := a.run([]string{"login", "--token", "--profile", "dev", "--url", server.URL})
	if err != nil || value != nil {
		t.Fatalf("got %v %v", value, err)
	}
	if !strings.Contains(out.String(), "▀") || !strings.Contains(out.String(), "Logged in.") {
		t.Fatalf("got %q", out.String())
	}
}
