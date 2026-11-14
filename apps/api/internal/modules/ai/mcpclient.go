package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// A minimal MCP streamable-HTTP client for `mcp` hooks: initialize, initialized, tools/call (rmcp does the same
// handshake in Rust). shortcut: failure texts approximate rmcp's wording; upgrade if clients parse last_error.

type mcpSession struct {
	url, token, sid string
	next            int
}

// httpStatusErr is rendered the way rmcp words a rejected initialize request.
type httpStatusErr struct{ msg string }

func (e httpStatusErr) Error() string { return e.msg }

func (s *mcpSession) post(ctx context.Context, payload map[string]any) (json.RawMessage, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	if s.sid != "" {
		req.Header.Set("Mcp-Session-Id", s.sid)
	}
	res, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if id := res.Header.Get("Mcp-Session-Id"); id != "" {
		s.sid = id
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, httpStatusErr{fmt.Sprintf("HTTP %s: %s", statusLine(res.StatusCode), b)}
	}
	if _, isCall := payload["id"]; !isCall {
		return nil, nil // notification: nothing to read
	}
	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if mt != "text/event-stream" {
		body, err := io.ReadAll(res.Body)
		return body, err
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "" && len(data) > 0:
			msg := json.RawMessage(strings.Join(data, "\n"))
			data = nil
			var probe struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(msg, &probe) == nil && len(probe.ID) > 0 && string(probe.ID) == fmt.Sprint(payload["id"]) {
				return msg, nil
			}
		}
	}
	if len(data) > 0 {
		return json.RawMessage(strings.Join(data, "\n")), nil
	}
	return nil, errors.New("stream ended without a response")
}

func (s *mcpSession) rpc(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	s.next++
	raw, err := s.post(ctx, map[string]any{"jsonrpc": "2.0", "id": s.next, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	var v struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil, errors.New("unexpected response")
	}
	if v.Error != nil {
		return nil, fmt.Errorf("Mcp error: %d: %s", v.Error.Code, v.Error.Message)
	}
	return v.Result, nil
}

func callMCP(ctx context.Context, url, tool string, arguments []byte, token *string, body any) error {
	s := &mcpSession{url: url, token: deref(token)}
	if _, err := s.rpc(ctx, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "cnote", "version": "0.1.0"}}); err != nil {
		return connectErr(err)
	}
	if _, err := s.post(ctx, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return connectErr(err)
	}
	args := map[string]any{}
	_ = json.Unmarshal(arguments, &args)
	args["cnote"] = body
	res, err := s.rpc(ctx, "tools/call", map[string]any{"name": tool, "arguments": args, "_meta": map[string]any{"progressToken": 0}})
	if s.sid != "" { // best effort: end the session
		req, _ := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodDelete, url, nil)
		req.Header.Set("Mcp-Session-Id", s.sid)
		if s.sid != "" && token != nil {
			req.Header.Set("Authorization", "Bearer "+*token)
		}
		if r, e := httpClient().Do(req); e == nil {
			r.Body.Close()
		}
	}
	if err != nil {
		return fmt.Errorf("MCP call failed: %v", err)
	}
	if res["isError"] == true {
		b, _ := json.Marshal(res["content"])
		return fmt.Errorf("MCP tool error: %s", b)
	}
	return nil
}

func connectErr(err error) error {
	var h httpStatusErr
	if errors.As(err, &h) {
		return fmt.Errorf("MCP connect failed: Send message error Transport [rmcp::transport::worker::WorkerTransport<rmcp::transport::streamable_http_client::StreamableHttpClientWorker<reqwest::async_impl::client::Client>>] error: unexpected server response: %s, when send initialize request", h.msg)
	}
	return fmt.Errorf("MCP connect failed: %v", err)
}
