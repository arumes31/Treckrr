package pdftext

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

// fixturePDF isolates font-map resolution from the application renderer. Real
// generated PDFs are covered by invoice renderer and HTTP integration tests.
func fixturePDF(t *testing.T, compressed bool) []byte {
	t.Helper()
	stream := func(font, x, y string) string {
		content := []byte("BT\n" + x + " " + y + " TD\n/F" + font + " 10 Tf 0 Tc 0 Ts\n[<0041>] TJ\nET\n")
		filter := ""
		if compressed {
			var buf bytes.Buffer
			writer := zlib.NewWriter(&buf)
			if _, err := writer.Write(content); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			content = buf.Bytes()
			filter = "/Filter/FlateDecode\n"
		}
		return fmt.Sprintf("<<%s/Length %d>>\nstream\n%s\nendstream", filter, len(content), content)
	}
	return []byte(fmt.Sprintf(`%%PDF-1.7
1 0 obj
<</Font << /F1 2 0 R /F2 3 0 R >>>>
endobj
2 0 obj
<</ToUnicode 4 0 R>>
endobj
3 0 obj
<</ToUnicode 5 0 R>>
endobj
4 0 obj
<0041><0041><0041>
endobj
5 0 obj
<0041><0041><03A9>
endobj
6 0 obj
<</Type /Page
/Contents 7 0 R
>>
endobj
7 0 obj
%s
endobj
8 0 obj
<</Type /Page
/Contents 9 0 R
>>
endobj
9 0 obj
%s
endobj
%%%%EOF
`, stream("1", "10", "700"), stream("2", "20", "300")))
}

func TestExtract(t *testing.T) {
	t.Parallel()
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprintf("compressed_%v", compressed), func(t *testing.T) {
			t.Parallel()
			data := fixturePDF(t, compressed)
			runs, err := Extract(data)
			if err != nil {
				t.Fatal(err)
			}
			want := []Run{{Text: "A", Page: 1, X: 10, Y: 700}, {Text: "Ω", Page: 2, X: 20, Y: 300}}
			if len(runs) != len(want) {
				t.Fatalf("runs = %+v", runs)
			}
			for i := range want {
				if runs[i] != want[i] {
					t.Errorf("run %d = %+v, want %+v", i, runs[i], want[i])
				}
			}
			text, err := Text(data)
			if err != nil || text != "A\nΩ" {
				t.Fatalf("Text = %q, error %v", text, err)
			}
		})
	}
}

func TestExtractRejectsIncompleteOrUnsupportedData(t *testing.T) {
	t.Parallel()
	fixture := string(fixturePDF(t, false))
	tests := []struct {
		name string
		data string
	}{
		{name: "missing header", data: "not a PDF"},
		{name: "no objects", data: "%PDF-1.7"},
		{name: "no font map", data: strings.Replace(fixture, "/ToUnicode 4 0 R", "", 1)},
		{name: "unsupported range", data: strings.Replace(fixture, "<0041><0041><0041>", "<0041><0042><0041>", 1)},
		{name: "missing page stream", data: strings.Replace(fixture, "/Contents 7 0 R", "", 1)},
		{name: "missing stream", data: strings.Replace(fixture, "stream\n", "data\n", 1)},
		{name: "truncated stream", data: strings.Replace(fixture, "endstream", "enddata", 1)},
		{name: "invalid compression", data: strings.Replace(fixture, "/Length", "/Filter/FlateDecode /Length", 1)},
		{name: "unsupported operation", data: strings.Replace(fixture, "TJ", "Tj", 1)},
		{name: "incomplete glyph", data: strings.Replace(fixture, "[<0041>]", "[<004>]", 1)},
		{name: "unmapped glyph", data: strings.Replace(fixture, "[<0041>]", "[<0042>]", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if text, err := Text([]byte(tt.data)); err == nil || text != "" {
				t.Fatalf("malformed PDF returned %q, error %v", text, err)
			}
		})
	}
}
