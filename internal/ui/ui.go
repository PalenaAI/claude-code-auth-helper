// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package ui styles ccauth's human-facing output: status lines, headings,
// summary cards, and highlighted commands and values. Everything is printed
// through lipgloss, which downsamples colors to what the terminal supports and
// strips them when the writer isn't a terminal or NO_COLOR is set, so piped
// output and logs stay plain.
//
// Never use this for the credential `ccauth token` prints to stdout.
package ui

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// The 16 basic ANSI colors follow the user's terminal theme, so they read well
// on light and dark backgrounds alike.
var (
	accent = lipgloss.Color("6") // cyan
	green  = lipgloss.Color("2")
	yellow = lipgloss.Color("3")
	red    = lipgloss.Color("1")
	blue   = lipgloss.Color("4")

	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	hintStyle    = lipgloss.NewStyle().Faint(true)
	valueStyle   = lipgloss.NewStyle().Foreground(accent)
	cmdStyle     = lipgloss.NewStyle().Foreground(accent).Bold(true)
	cardStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)

	glyphs = map[string]lipgloss.Style{
		"✓": lipgloss.NewStyle().Foreground(green).Bold(true),
		"✗": lipgloss.NewStyle().Foreground(red).Bold(true),
		"⚠": lipgloss.NewStyle().Foreground(yellow).Bold(true),
		"•": lipgloss.NewStyle().Foreground(blue),
	}
)

// NoColor reports whether colors are switched off (NO_COLOR, or --no-color,
// which sets it).
func NoColor() bool { return os.Getenv("NO_COLOR") != "" }

// Cmd styles a command the user should run.
func Cmd(s string) string { return cmdStyle.Render(s) }

// Val styles a value to copy or check: a URI, path, ID or key.
func Val(s string) string { return valueStyle.Render(s) }

// Bold emphasizes text.
func Bold(s string) string { return boldStyle.Render(s) }

// Hint de-emphasizes secondary text.
func Hint(s string) string { return hintStyle.Render(s) }

// Glyph styles a status glyph (✓ ✗ ⚠ •); other strings pass through.
func Glyph(g string) string {
	if st, ok := glyphs[g]; ok {
		return st.Render(g)
	}
	return g
}

// Status prints "<glyph> msg". Commands quoted in backticks are highlighted
// on a color terminal; elsewhere the backticks stay so the text still reads.
func Status(w io.Writer, glyph, msg string) {
	_, _ = lipgloss.Fprintln(w, Glyph(glyph)+" "+Commands(w, msg))
}

var backticked = regexp.MustCompile("`([^`]+)`")

// Commands highlights `quoted` commands in msg for a color-capable w.
func Commands(w io.Writer, msg string) string {
	if !colorful(w) {
		return msg
	}
	return backticked.ReplaceAllStringFunc(msg, func(m string) string { return Cmd(m[1 : len(m)-1]) })
}

// colorful reports whether w renders colors (a terminal, colors not disabled).
func colorful(w io.Writer) bool {
	return colorprofile.Detect(w, os.Environ()) > colorprofile.Ascii
}

// OK, Warn, Fail and Info print a status line with formatting.
func OK(w io.Writer, format string, a ...any)   { Status(w, "✓", fmt.Sprintf(format, a...)) }
func Warn(w io.Writer, format string, a ...any) { Status(w, "⚠", fmt.Sprintf(format, a...)) }
func Fail(w io.Writer, format string, a ...any) { Status(w, "✗", fmt.Sprintf(format, a...)) }
func Info(w io.Writer, format string, a ...any) { Status(w, "•", fmt.Sprintf(format, a...)) }

// Heading prints a section heading preceded by a blank line.
func Heading(w io.Writer, text string) {
	_, _ = lipgloss.Fprintln(w, "\n"+headingStyle.Render(text))
}

// Println prints styled text, downsampled for w.
func Println(w io.Writer, a ...any) { _, _ = lipgloss.Fprintln(w, a...) }

// Printf prints formatted styled text, downsampled for w.
func Printf(w io.Writer, format string, a ...any) { _, _ = lipgloss.Fprintf(w, format, a...) }

// Card prints a bordered box with a bold title and the given lines. Without
// colors it falls back to an unboxed, indented block that copies cleanly.
func Card(w io.Writer, title string, lines ...string) {
	if NoColor() {
		// lipgloss strips the styling inside the lines.
		_, _ = lipgloss.Fprintln(w, "\n"+title)
		for _, l := range lines {
			_, _ = lipgloss.Fprintln(w, "  "+l)
		}
		return
	}
	body := boldStyle.Render(title)
	if len(lines) > 0 {
		body += "\n\n" + strings.Join(lines, "\n")
	}
	_, _ = lipgloss.Fprintln(w, cardStyle.Render(body))
}

// KV renders "label  value" rows with aligned labels, for cards and status.
func KV(rows ...[2]string) []string {
	width := 0
	for _, r := range rows {
		if len(r[0]) > width {
			width = len(r[0])
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, Hint(fmt.Sprintf("%-*s", width, r[0]))+"  "+r[1])
	}
	return out
}
