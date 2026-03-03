package extract

import (
	"context"
	"net/url"
)

// Strategy extracts structured content from raw HTML.
// The existing Extractor satisfies this interface.
type Strategy interface {
	Extract(ctx context.Context, body []byte, pageURL *url.URL) (*Result, error)
}
