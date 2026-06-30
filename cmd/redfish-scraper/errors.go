package main

import "strings"

// scrapeErrors collects every error encountered while scraping. Unlike
// errors.Join, which renders all errors as a single space-joined line, it
// prints one error per line so failures are easy to scan.
type scrapeErrors []error

func (e scrapeErrors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}

	return strings.Join(msgs, "\n")
}

func (e scrapeErrors) Unwrap() []error {
	return e
}
