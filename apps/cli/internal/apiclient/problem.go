package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// ProblemError is an RFC 9457 problem document the API answered with, the
// shape Huma writes for every failure. Callers branch on Status and, for a
// validation failure, on HasDetail, never on the wording of Detail, which the
// API is free to change.
//
// Status always comes from the HTTP response and never from the body, so it
// is set even when the body is not a problem document at all. A reverse proxy
// in front of the API answers with HTML, and that must still read as "502".
type ProblemError struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Errors lists the individual faults of a validation failure.
	Errors []ProblemDetail `json:"errors"`
}

// ProblemDetail is one fault inside a problem document.
type ProblemDetail struct {
	// Location says where the fault is, in Huma's notation: "body.password",
	// "query.fg", "path.domain_id".
	Location string `json:"location"`
	Message  string `json:"message"`
	// Value is whatever the API attached: the rejected input, or a typed
	// token such as "low_contrast". It is a json.Number when the API sent a
	// number, so a count such as 12 is not turned into the float 12.
	Value any `json:"value"`
}

// Error describes the failure for a log or a fallback message. It carries
// the status, the title (the status text when the body gave none) and the
// detail, and never a header or a token. The CLI's own, translated text is
// the command layer's business.
func (p *ProblemError) Error() string {
	title := p.Title
	if title == "" {
		title = http.StatusText(p.Status)
	}
	msg := fmt.Sprintf("the API answered %d %s", p.Status, title)
	if p.Detail != "" {
		msg += ": " + p.Detail
	}
	return msg
}

// HasDetail reports whether the problem lists a fault at location whose value
// is value. An empty value matches any value at that location, which is how a
// caller asks "is there a fault on query.fg" without caring which one. A
// nil receiver has no details.
//
// The match is on the value's text, so a number compares as the digits the
// API sent: HasDetail("path.domain_id", "3").
func (p *ProblemError) HasDetail(location, value string) bool {
	if p == nil {
		return false
	}
	for _, d := range p.Errors {
		if d.Location != location {
			continue
		}
		if value == "" || valueText(d.Value) == value {
			return true
		}
	}
	return false
}

// valueText renders a detail's value for comparison: strings as they are,
// numbers as written, anything else as compact JSON.
func valueText(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		out, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(out)
	}
}

// decodeProblem builds the error for a non-2xx answer. A body that is not a
// problem document leaves everything but Status empty.
func decodeProblem(status int, body []byte) *ProblemError {
	p := &ProblemError{}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(p); err != nil {
		// Decode may have filled part of the struct before it failed; a
		// half-read document is worse than none.
		p = &ProblemError{}
	}
	p.Status = status
	return p
}
