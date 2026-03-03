package extract

import "net/url"

// Strategy extracts structured content from raw HTML.
// The existing Extractor satisfies this interface.
type Strategy interface {
	Extract(body []byte, pageURL *url.URL) (*Result, error)
}
