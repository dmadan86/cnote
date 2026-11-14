package hooks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/netguard"
)

// mcpClient is the minimal MCP streamable-HTTP client rmcp provides in Rust: one session, one tool call.
type mcpClient struct {
	hc      *http.Client
	url     string
	token   *string
	session string
	version string
	nextID  int
}

func (m *mcpClient) do(ctx context.Context, method string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := command.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.url, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if m.token != nil {
		req.Header.Set("Authorization", "Bearer "+*m.token)
	}
	if m.session != "" {
		req.Header.Set("Mcp-Session-Id", m.session)
	}
	if m.version != "" {
		req.Header.Set("MCP-Protocol-Version", m.version)
	}
	return m.hc.Do(req)
}

// rpc sends a request and returns the JSON-RPC result of the reply with our id (JSON or SSE).
func (m *mcpClient) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.nextID++
	res, err := m.do(ctx, "POST", map[string]any{"jsonrpc": "2.0", "id": m.nextID, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if s := res.Header.Get("Mcp-Session-Id"); s != "" {
		m.session = s
	}
	var msgs [][]byte
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(nil, 16<<20)
		var data []string
		flush := func() {
			if len(data) > 0 {
				msgs = append(msgs, []byte(strings.Join(data, "\n")))
				data = nil
			}
		}
		for sc.Scan() {
			l := strings.TrimRight(sc.Text(), "\r")
			if l == "" {
				flush()
			} else if v, ok := strings.CutPrefix(l, "data:"); ok {
				data = append(data, strings.TrimPrefix(v, " "))
			}
		}
		flush()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	} else {
		b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
		if err != nil {
			return nil, err
		}
		msgs = [][]byte{b}
	}
	for _, b := range msgs {
		var r struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &r) != nil || r.ID == nil || *r.ID != m.nextID {
			continue // notifications and batches of other ids
		}
		if r.Error != nil {
			return nil, errors.New(r.Error.Message)
		}
		return r.Result, nil
	}
	return nil, errors.New("no response")
}

func (m *mcpClient) notify(ctx context.Context, method string) error {
	res, err := m.do(ctx, "POST", map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

func callMCP(ctx context.Context, t target, token *string, body json.RawMessage) error {
	m := &mcpClient{hc: netguard.Client(), url: t.URL, token: token}
	init, err := m.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "cnote_ai", "version": "0.1.0"},
	})
	if err != nil {
		return fmt.Errorf("MCP connect failed: %v", err)
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(init, &ir)
	m.version = ir.ProtocolVersion
	// Best-effort session end, also when the call fails; use a fresh deadline so a cancelled ctx still closes it.
	defer func() {
		if m.session == "" {
			return
		}
		if res, err := m.do(context.WithoutCancel(ctx), "DELETE", nil); err == nil {
			res.Body.Close()
		}
	}()
	if err := m.notify(ctx, "notifications/initialized"); err != nil {
		return fmt.Errorf("MCP connect failed: %v", err)
	}
	args := map[string]any{}
	if len(t.Arguments) > 0 {
		_ = json.Unmarshal(t.Arguments, &args)
		if args == nil {
			args = map[string]any{}
		}
	}
	args["cnote"] = body
	tool := ""
	if t.Tool != nil {
		tool = *t.Tool
	}
	out, err := m.rpc(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return fmt.Errorf("MCP call failed: %v", err)
	}
	var r struct {
		IsError bool            `json:"isError"`
		Content json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(out, &r)
	if r.IsError {
		c, err := command.Norm(orNull(r.Content))
		if err != nil {
			c = json.RawMessage("null")
		}
		return fmt.Errorf("MCP tool error: %s", c)
	}
	return nil
}
