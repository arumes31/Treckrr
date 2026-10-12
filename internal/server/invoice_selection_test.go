package server

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestedInvoiceID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, query, body string
		id                int64
		present, invalid  bool
	}{
		{name: "absent"},
		{name: "query", query: "invoice_id=42", id: 42, present: true},
		{name: "post", body: "invoice_id=42", id: 42, present: true},
		{name: "empty", query: "invoice_id=", present: true, invalid: true},
		{name: "zero", query: "invoice_id=0", present: true, invalid: true},
		{name: "negative", query: "invoice_id=-1", present: true, invalid: true},
		{name: "space", query: "invoice_id=+1", present: true, invalid: true},
		{name: "overflow", query: "invoice_id=9223372036854775808", present: true, invalid: true},
		{name: "duplicate", query: "invoice_id=1&invoice_id=2", present: true, invalid: true},
		{name: "conflicting sources", query: "invoice_id=1", body: "invoice_id=2", present: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/?"+tc.query, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			id, present, err := requestedInvoiceID(r)
			if id != tc.id || present != tc.present || (err != nil) != tc.invalid {
				t.Fatalf("selection = (%d,%v,%v), want (%d,%v,invalid=%v)", id, present, err, tc.id, tc.present, tc.invalid)
			}
		})
	}
}

func TestRequestedInvoiceIDMultipart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, query string
		values      []string
		invalid     bool
	}{
		{name: "body selection", values: []string{"42"}},
		{name: "duplicate body", values: []string{"42", "43"}, invalid: true},
		{name: "query and body", query: "invoice_id=43", values: []string{"42"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for _, value := range tc.values {
				if err := writer.WriteField("invoice_id", value); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/?"+tc.query, &body)
			r.Header.Set("Content-Type", writer.FormDataContentType())
			id, present, err := requestedInvoiceID(r)
			if !present || (err != nil) != tc.invalid || (!tc.invalid && id != 42) {
				t.Fatalf("multipart selection = (%d,%v,%v), invalid=%v", id, present, err, tc.invalid)
			}
		})
	}
	for _, contentType := range []string{
		"multipart/form-data",
		"multipart/form-data; boundary=missing",
	} {
		r := httptest.NewRequest("POST", "/?invoice_id=42", strings.NewReader("malformed body"))
		r.Header.Set("Content-Type", contentType)
		if id, _, err := requestedInvoiceID(r); err == nil || id != 0 {
			t.Fatalf("malformed multipart accepted for %q: id=%d err=%v", contentType, id, err)
		}
	}
}
