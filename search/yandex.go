package search

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anatolykoptev/go-engine/metrics"
	"github.com/anatolykoptev/go-engine/sources"
)

const (
	metricYandexRequests = "yandex_requests"
	yandexAsyncEndpoint  = "https://searchapi.api.cloud.yandex.net/v2/web/searchAsync"
	yandexOpEndpoint     = "https://operation.api.cloud.yandex.net/operations/"
	yandexPollInterval   = 500 * time.Millisecond
	yandexMaxPollWait    = 10 * time.Second
)

// YandexConfig holds Yandex Search API v2 credentials.
type YandexConfig struct {
	APIKey   string // Api-Key for Authorization header
	FolderID string // Yandex Cloud folder ID
}

// yandexRequest is the JSON body for searchAsync.
type yandexRequest struct {
	Query     yandexQuery     `json:"query"`
	SortSpec  yandexSortSpec  `json:"sortSpec"`
	GroupSpec yandexGroupSpec `json:"groupSpec"`
	MaxPass   string          `json:"maxPassages"`
	Region    string          `json:"region"`
	L10N      string          `json:"l10N"`
	FolderID  string          `json:"folderId"`
	Page      string          `json:"page"`
}

type yandexQuery struct {
	SearchType string `json:"searchType"`
	QueryText  string `json:"queryText"`
	FamilyMode string `json:"familyMode"`
}

type yandexSortSpec struct {
	SortMode  string `json:"sortMode"`
	SortOrder string `json:"sortOrder"`
}

type yandexGroupSpec struct {
	GroupMode    string `json:"groupMode"`
	GroupsOnPage string `json:"groupsOnPage"`
	DocsInGroup  string `json:"docsInGroup"`
}

// yandexOperation is the async operation response.
type yandexOperation struct {
	ID       string          `json:"id"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response"`
	Error    *yandexError    `json:"error"`
}

type yandexError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// yandexXMLResponse is the XML search result envelope.
type yandexXMLResponse struct {
	XMLName  xml.Name       `xml:"yandexsearch"`
	Response yandexRespBody `xml:"response"`
}

type yandexRespBody struct {
	Error   *yandexXMLError  `xml:"error"`
	Results yandexXMLResults `xml:"results"`
}

type yandexXMLError struct {
	Code    int    `xml:"code,attr"`
	Message string `xml:",chardata"`
}

type yandexXMLResults struct {
	Grouping yandexXMLGrouping `xml:"grouping"`
}

type yandexXMLGrouping struct {
	Groups []yandexXMLGroup `xml:"group"`
}

type yandexXMLGroup struct {
	Docs []yandexXMLDoc `xml:"doc"`
}

type yandexXMLDoc struct {
	URL      string            `xml:"url"`
	Domain   string            `xml:"domain"`
	Title    string            `xml:"title"`
	Headline string            `xml:"headline"`
	Passages yandexXMLPassages `xml:"passages"`
}

type yandexXMLPassages struct {
	Passage []string `xml:"passage"`
}

// SearchYandexAPI queries Yandex Search API v2 (async) and returns results.
func SearchYandexAPI(ctx context.Context, cfg YandexConfig, query, region string, m *metrics.Registry) ([]sources.Result, error) {
	if cfg.APIKey == "" || cfg.FolderID == "" {
		return nil, errors.New("yandex: api key and folder_id required")
	}

	if region == "" {
		region = "213" // Moscow
	}

	if m != nil {
		m.Incr(metricYandexRequests)
	}

	// 1. Start async search operation.
	opID, err := yandexStartSearch(ctx, cfg, query, region)
	if err != nil {
		return nil, fmt.Errorf("yandex start: %w", err)
	}

	// 2. Poll for completion.
	xmlData, err := yandexPollOperation(ctx, cfg, opID)
	if err != nil {
		return nil, fmt.Errorf("yandex poll: %w", err)
	}

	// 3. Parse XML response.
	results, err := ParseYandexXML(xmlData)
	if err != nil {
		return nil, fmt.Errorf("yandex parse: %w", err)
	}

	slog.Debug("yandex api results", slog.Int("count", len(results)), slog.String("query", query))
	return results, nil
}

func yandexStartSearch(ctx context.Context, cfg YandexConfig, query, region string) (string, error) {
	reqBody := yandexRequest{
		Query: yandexQuery{
			SearchType: "SEARCH_TYPE_RU",
			QueryText:  query,
			FamilyMode: "FAMILY_MODE_NONE",
		},
		SortSpec: yandexSortSpec{
			SortMode:  "SORT_MODE_BY_RELEVANCE",
			SortOrder: "SORT_ORDER_DESC",
		},
		GroupSpec: yandexGroupSpec{
			GroupMode:    "GROUP_MODE_DEEP",
			GroupsOnPage: "10",
			DocsInGroup:  "1",
		},
		MaxPass:  "2",
		Region:   region,
		L10N:     "LOCALIZATION_RU",
		FolderID: cfg.FolderID,
		Page:     "0",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yandexAsyncEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Api-Key "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("yandex async HTTP %d: %s", resp.StatusCode, string(data))
	}

	var op yandexOperation
	if err := json.Unmarshal(data, &op); err != nil {
		return "", fmt.Errorf("yandex json: %w", err)
	}

	if op.ID == "" {
		return "", errors.New("yandex: empty operation id")
	}

	return op.ID, nil
}

func yandexPollOperation(ctx context.Context, cfg YandexConfig, opID string) ([]byte, error) {
	deadline := time.Now().Add(yandexMaxPollWait)
	ticker := time.NewTicker(yandexPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("yandex: operation %s timed out", opID)
			}
		}

		op, err := yandexFetchOperation(ctx, cfg, opID)
		if err != nil {
			return nil, err
		}
		if !op.Done {
			continue
		}
		return yandexExtractResponse(op)
	}
}

// yandexFetchOperation polls a single operation status.
func yandexFetchOperation(ctx context.Context, cfg YandexConfig, opID string) (*yandexOperation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, yandexOpEndpoint+opID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Api-Key "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yandex poll HTTP %d: %s", resp.StatusCode, string(data))
	}

	var op yandexOperation
	if err := json.Unmarshal(data, &op); err != nil {
		return nil, fmt.Errorf("yandex poll json: %w", err)
	}

	if op.Error != nil {
		return nil, fmt.Errorf("yandex error %d: %s", op.Error.Code, op.Error.Message)
	}

	return &op, nil
}

// yandexExtractResponse extracts XML bytes from a completed operation response.
func yandexExtractResponse(op *yandexOperation) ([]byte, error) {
	if len(op.Response) == 0 {
		return nil, errors.New("yandex: empty response in completed operation")
	}

	// Try JSON-wrapped base64: {"rawData": "base64..."}
	var wrapped struct {
		RawData string `json:"rawData"`
	}
	if err := json.Unmarshal(op.Response, &wrapped); err == nil && wrapped.RawData != "" {
		return base64.StdEncoding.DecodeString(wrapped.RawData)
	}

	// Try direct XML string in JSON.
	var xmlStr string
	if err := json.Unmarshal(op.Response, &xmlStr); err == nil {
		return []byte(xmlStr), nil
	}

	return op.Response, nil
}

// ParseYandexXML extracts search results from Yandex Search API XML response.
func ParseYandexXML(data []byte) ([]sources.Result, error) {
	var resp yandexXMLResponse
	if err := xml.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("xml unmarshal: %w", err)
	}

	if resp.Response.Error != nil {
		return nil, fmt.Errorf("yandex error %d: %s", resp.Response.Error.Code, resp.Response.Error.Message)
	}

	var results []sources.Result
	for _, group := range resp.Response.Results.Grouping.Groups {
		for _, doc := range group.Docs {
			if doc.URL == "" {
				continue
			}

			title := cleanXMLText(doc.Title)
			if title == "" {
				title = doc.Headline
			}

			content := doc.Headline
			if len(doc.Passages.Passage) > 0 {
				content = strings.Join(doc.Passages.Passage, " ")
			}
			content = cleanXMLText(content)

			results = append(results, sources.Result{
				Title:    title,
				Content:  content,
				URL:      doc.URL,
				Score:    directResultScore,
				Metadata: map[string]string{"engine": "yandex"},
			})
		}
	}

	return results, nil
}

// cleanXMLText strips Yandex highlight tags from text.
func cleanXMLText(s string) string {
	s = strings.ReplaceAll(s, "<hlword>", "")
	s = strings.ReplaceAll(s, "</hlword>", "")
	return strings.TrimSpace(s)
}
