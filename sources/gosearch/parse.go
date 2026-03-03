package gosearch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// extractSSEData reads an SSE stream and returns the last "data:" payload.
// MCP Streamable HTTP sends "event: message\ndata: {json}\n\n".
func extractSSEData(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, maxResponseBytes), maxResponseBytes)
	var lastData string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			lastData = after
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE stream: %w", err)
	}
	if lastData == "" {
		return nil, errors.New("no data in SSE response")
	}
	return []byte(lastData), nil
}

// parseRPCResponse extracts search results from a JSON-RPC response.
// The MCP response has: result.content[0].text = JSON-encoded search output.
func parseRPCResponse(data []byte) ([]Result, error) {
	var rpcResp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse rpc response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if len(rpcResp.Result.Content) == 0 {
		return nil, errors.New("empty content in rpc response")
	}

	var output struct {
		Results []Result `json:"results"`
	}
	if err := json.Unmarshal([]byte(rpcResp.Result.Content[0].Text), &output); err != nil {
		return nil, fmt.Errorf("parse search output: %w", err)
	}

	return output.Results, nil
}
