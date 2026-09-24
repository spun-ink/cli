package main

import (
	"embed"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Bobbin, the spun.ink mascot. The grids come from the art the website draws him with. He is drawn
// only at a terminal, never into output an agent parses.
//
//go:embed bobbin/palette.yml bobbin/digging.txt bobbin/shipping.txt
var bobbinFiles embed.FS

// bobbinWanted: a terminal on stdout, and nobody asked for plain text.
func bobbinWanted() bool {
	return isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && ansiColour(os.Stdout)
}

func bobbinPalette() map[byte][3]uint8 {
	data, _ := bobbinFiles.ReadFile("bobbin/palette.yml")
	colors := map[byte][3]uint8{}
	for _, line := range strings.Split(string(data), "\n") {
		// "V": "#6C4BF5"   # violet
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		if len(key) != 3 || !strings.HasPrefix(value, `"#`) || len(value) < 9 {
			continue
		}
		n, _ := strconv.ParseUint(value[2:8], 16, 32)
		colors[key[1]] = [3]uint8{uint8(n >> 16), uint8(n >> 8), uint8(n)}
	}
	return colors
}

// sceneGrid is the first frame of a scene, cropped to the figure: the ground (rows filled edge to
// edge) and the empty margins go, so Bobbin stays small beside the text.
func sceneGrid(name string) []string {
	data, _ := bobbinFiles.ReadFile("bobbin/" + name + ".txt")
	var rows []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "---" {
			break
		}
		if line == "" || strings.HasPrefix(line, "#") || !strings.ContainsRune(line, '.') {
			continue
		}
		rows = append(rows, line)
	}
	top, bottom, left, right := len(rows), -1, len(rows[0]), -1
	for y, row := range rows {
		for x := range row {
			if row[x] != '.' {
				top, bottom = min(top, y), max(bottom, y)
				left, right = min(left, x), max(right, x)
			}
		}
	}
	cropped := make([]string, 0, bottom-top+1)
	for _, row := range rows[top : bottom+1] {
		cropped = append(cropped, row[left:right+1])
	}
	return cropped
}

// drawBobbin renders a scene with half blocks: one terminal row carries two pixel rows, the upper
// as foreground of ▀ and the lower as its background, in 24-bit colour.
func drawBobbin(name string) []string {
	colors := bobbinPalette()
	grid := sceneGrid(name)
	if len(grid)%2 == 1 {
		grid = append(grid, strings.Repeat(".", len(grid[0])))
	}
	lines := make([]string, 0, len(grid)/2)
	for y := 0; y < len(grid); y += 2 {
		var line strings.Builder
		for x := range grid[y] {
			upper, lower := grid[y][x], grid[y+1][x]
			switch {
			case upper == '.' && lower == '.':
				line.WriteString("\x1b[0m ")
			case lower == '.':
				c := colors[upper]
				fmt.Fprintf(&line, "\x1b[0;38;2;%d;%d;%dm▀", c[0], c[1], c[2])
			case upper == '.':
				c := colors[lower]
				fmt.Fprintf(&line, "\x1b[0;38;2;%d;%d;%dm▄", c[0], c[1], c[2])
			default:
				u, l := colors[upper], colors[lower]
				fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm▀", u[0], u[1], u[2], l[0], l[1], l[2])
			}
		}
		line.WriteString("\x1b[0m")
		lines = append(lines, line.String())
	}
	return lines
}

// beside puts text to the right of Bobbin, vertically centred on him.
func beside(scene string, text ...string) string {
	art := drawBobbin(scene)
	offset := max(0, (len(art)-len(text))/2)
	var out strings.Builder
	for i, line := range art {
		out.WriteString(line)
		if j := i - offset; j >= 0 && j < len(text) {
			out.WriteString("   " + text[j])
		}
		out.WriteString("\n")
	}
	return out.String()
}
