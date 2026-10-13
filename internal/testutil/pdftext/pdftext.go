// Package pdftext extracts text from the restricted PDF dialect emitted by our
// gopdf renderers for tests. It is not a general-purpose or untrusted PDF reader.
package pdftext

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Run is one text operation, with a one-based page and PDF baseline coordinates
// in points (Y is measured from the bottom of the page).
type Run struct {
	Text string
	Page int
	X    float64
	Y    float64
}

var (
	objectsRE = regexp.MustCompile(`(?s)(\d+) 0 obj\b(.*?)endobj`)
	fontRE    = regexp.MustCompile(`/F(\d+) (\d+) 0 R`)
	unicodeRE = regexp.MustCompile(`/ToUnicode (\d+) 0 R`)
	glyphRE   = regexp.MustCompile(`<([0-9A-F]{4})><([0-9A-F]{4})><([0-9A-F]{4})>`)
	pageRE    = regexp.MustCompile(`/Type /Page\b`)
	contentRE = regexp.MustCompile(`/Contents\s+([^\n]+)`)
	refRE     = regexp.MustCompile(`(\d+) 0 R`)
	textRE    = regexp.MustCompile(`(?s)BT\s+(-?[\d.]+) (-?[\d.]+) TD\s+/F(\d+) .*?\[(.*?)\] TJ\s+ET`)
	hexRE     = regexp.MustCompile(`<([0-9A-F]*)>`)
)

// Text extracts visible text in drawing order, separated by newlines.
func Text(data []byte) (string, error) {
	runs, err := Extract(data)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(runs))
	for _, run := range runs {
		lines = append(lines, run.Text)
	}
	return strings.Join(lines, "\n"), nil
}

// Extract decodes the actual page streams through the embedded font CMaps.
// Unsupported or incomplete documents fail instead of silently losing text.
func Extract(data []byte) ([]Run, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("pdftext: missing PDF header")
	}
	matches := objectsRE.FindAllSubmatch(data, -1)
	objects := make(map[string][]byte, len(matches))
	for _, match := range matches {
		objects[string(match[1])] = match[2]
	}
	fonts := make(map[string]map[string]string)
	for _, match := range fontRE.FindAllSubmatch(data, -1) {
		font := objects[string(match[2])]
		unicode := unicodeRE.FindSubmatch(font)
		if unicode == nil {
			return nil, fmt.Errorf("pdftext: font %s has no Unicode map", match[1])
		}
		cmap := objects[string(unicode[1])]
		glyphs := make(map[string]string)
		for _, glyph := range glyphRE.FindAllSubmatch(cmap, -1) {
			if !bytes.Equal(glyph[1], glyph[2]) {
				return nil, fmt.Errorf("pdftext: unsupported glyph range")
			}
			value, err := strconv.ParseUint(string(glyph[3]), 16, 16)
			if err != nil {
				return nil, fmt.Errorf("pdftext: decode glyph: %w", err)
			}
			glyphs[string(glyph[1])] = string(rune(value))
		}
		fonts[string(match[1])] = glyphs
	}
	runs := []Run{}
	page := 0
	for _, object := range matches {
		if !pageRE.Match(object[2]) {
			continue
		}
		page++
		contents := contentRE.FindSubmatch(object[2])
		if contents == nil {
			return nil, fmt.Errorf("pdftext: page %d has no content", page)
		}
		for _, ref := range refRE.FindAllSubmatch(contents[1], -1) {
			stream, err := decodedStream(objects[string(ref[1])])
			if err != nil {
				return nil, err
			}
			texts := textRE.FindAllSubmatch(stream, -1)
			if len(texts) != bytes.Count(stream, []byte("BT\n")) {
				return nil, fmt.Errorf("pdftext: unsupported text operation on page %d", page)
			}
			for _, text := range texts {
				run, err := decodeRun(text, fonts[string(text[3])], page)
				if err != nil {
					return nil, err
				}
				runs = append(runs, run)
			}
		}
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("pdftext: no text found")
	}
	return runs, nil
}

func decodedStream(object []byte) ([]byte, error) {
	_, body, ok := bytes.Cut(object, []byte("stream\n"))
	if !ok {
		return nil, fmt.Errorf("pdftext: content stream missing")
	}
	body, _, ok = bytes.Cut(body, []byte("endstream"))
	if !ok {
		return nil, fmt.Errorf("pdftext: content stream incomplete")
	}
	if !bytes.Contains(object, []byte("/FlateDecode")) {
		return body, nil
	}
	reader, err := zlib.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("pdftext: compressed stream: %w", err)
	}
	defer reader.Close()
	const limit = 16 << 20
	decoded, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("pdftext: decode stream: %w", err)
	}
	if len(decoded) > limit {
		return nil, fmt.Errorf("pdftext: test stream too large")
	}
	return decoded, nil
}

func decodeRun(text [][]byte, glyphs map[string]string, page int) (Run, error) {
	x, err := strconv.ParseFloat(string(text[1]), 64)
	if err != nil {
		return Run{}, fmt.Errorf("pdftext: x coordinate: %w", err)
	}
	y, err := strconv.ParseFloat(string(text[2]), 64)
	if err != nil {
		return Run{}, fmt.Errorf("pdftext: y coordinate: %w", err)
	}
	var decoded strings.Builder
	for _, hex := range hexRE.FindAllSubmatch(text[4], -1) {
		encoded := string(hex[1])
		if len(encoded)%4 != 0 {
			return Run{}, fmt.Errorf("pdftext: incomplete glyph")
		}
		for i := 0; i < len(encoded); i += 4 {
			value, ok := glyphs[encoded[i:i+4]]
			if !ok {
				return Run{}, fmt.Errorf("pdftext: unmapped glyph %s", encoded[i:i+4])
			}
			decoded.WriteString(value)
		}
	}
	return Run{Text: decoded.String(), Page: page, X: x, Y: y}, nil
}
