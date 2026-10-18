package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testServer(drive *bool) *Server {
	type echoArgs struct {
		Text string `json:"text"`
	}
	s := New("owexplorer", "test", []Tool{
		{Name: "echo", Description: "echo", Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var a echoArgs
			if err := Args(raw, &a); err != nil {
				return nil, err
			}
			return map[string]string{"text": a.Text}, nil
		}},
		{Name: "fail", Handler: func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("boom") }},
		{Name: "big", Handler: func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("x", MaxResult+1), nil }},
		{Name: "panic", Handler: func(context.Context, json.RawMessage) (any, error) { panic("oops") }},
		{Name: "pic", Handler: func(context.Context, json.RawMessage) (any, error) {
			return Content{Image([]byte{1, 2, 3}, "image/png"), Text("saved")}, nil
		}},
		{Name: "move", Drive: true, Handler: func(context.Context, json.RawMessage) (any, error) { return "moved", nil }},
	})
	s.DriveEnabled = func() bool { return *drive }
	return s
}

func post(t *testing.T, url, body string, hdr ...string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out)
	return r, out
}

// text is the tool result text of a JSON-RPC tools/call response.
func text(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp)
	}
	c := res["content"].([]any)[0].(map[string]any)
	return c["text"].(string), res["isError"].(bool)
}

func TestRPC(t *testing.T) {
	drive := false
	ts := httptest.NewServer(testServer(&drive).Handler())
	defer ts.Close()
	u := ts.URL + "/mcp"

	_, r := post(t, u, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	if res := r["result"].(map[string]any); res["protocolVersion"] != "2025-03-26" || res["serverInfo"].(map[string]any)["name"] != "owexplorer" {
		t.Fatalf("initialize: %v", r)
	}
	_, r = post(t, u, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if r["result"].(map[string]any)["protocolVersion"] != protocolVersions[0] {
		t.Fatalf("unknown version not answered with ours: %v", r)
	}
	if resp, _ := post(t, u, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification: %d", resp.StatusCode)
	}
	_, r = post(t, u, `{"jsonrpc":"2.0","id":"a","method":"tools/list"}`)
	tools := r["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 6 || r["id"] != "a" {
		t.Fatalf("tools/list: %v", r)
	}
	for _, x := range tools {
		m := x.(map[string]any)
		if m["name"] == "move" && !strings.Contains(m["description"].(string), "drive tool") {
			t.Fatalf("drive tool not marked: %v", m)
		}
		if m["inputSchema"].(map[string]any)["type"] != "object" {
			t.Fatalf("schema: %v", m)
		}
	}
	_, r = post(t, u, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	if s, isErr := text(t, r); isErr || s != `{"text":"hi"}` {
		t.Fatalf("echo: %q %v", s, isErr)
	}
	for name, want := range map[string]string{"fail": "boom", "big": "too large", "panic": "panic in panic", "move": "drive tools are off"} {
		_, r = post(t, u, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":%q}}`, name))
		if s, isErr := text(t, r); !isErr || !strings.Contains(s, want) {
			t.Fatalf("%s: %q %v", name, s, isErr)
		}
	}
	_, r = post(t, u, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"txt":"typo"}}}`)
	if s, isErr := text(t, r); !isErr || !strings.Contains(s, "unknown field") {
		t.Fatalf("strict args: %q", s)
	}
	_, r = post(t, u, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"pic"}}`)
	if c := r["result"].(map[string]any)["content"].([]any); len(c) != 2 || c[0].(map[string]any)["type"] != "image" ||
		c[0].(map[string]any)["data"] != "AQID" || c[0].(map[string]any)["mimeType"] != "image/png" || c[1].(map[string]any)["text"] != "saved" {
		t.Fatalf("content parts: %v", r)
	}
	drive = true
	_, r = post(t, u, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"move"}}`)
	if s, isErr := text(t, r); isErr || s != `"moved"` {
		t.Fatalf("drive on: %q", s)
	}
	_, r = post(t, u, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`)
	if r["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Fatalf("unknown tool: %v", r)
	}
	_, r = post(t, u, `{"jsonrpc":"2.0","id":7,"method":"resources/list"}`)
	if r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method: %v", r)
	}
	// batch
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/x"}]`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var batch []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&batch)
	resp.Body.Close()
	if len(batch) != 1 || batch[0]["id"].(float64) != 1 {
		t.Fatalf("batch: %v", batch)
	}
	// no SSE stream, browsers refused, JSON only
	if g, _ := http.Get(u); g.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp: %d", g.StatusCode)
	}
	if resp, _ := post(t, u, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", resp.StatusCode)
	}
	if resp, _ := post(t, u, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "http://localhost:3000"); resp.StatusCode != 200 {
		t.Fatalf("local origin: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, u, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "text/plain")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", resp.StatusCode)
	}
}

func TestPlainEndpoints(t *testing.T) {
	drive := false
	s := testServer(&drive)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_, r := post(t, ts.URL+"/mcp/tools/list", `{}`)
	if len(r["tools"].([]any)) != 6 {
		t.Fatalf("list: %v", r)
	}
	_, r = post(t, ts.URL+"/mcp/tools/call", `{"name":"echo","arguments":{"text":"x"}}`)
	if r["isError"] != false || r["result"].(map[string]any)["text"] != "x" {
		t.Fatalf("call: %v", r)
	}
	_, r = post(t, ts.URL+"/mcp/tools/call", `{"name":"fail"}`)
	if r["isError"] != true || r["error"] != "boom" {
		t.Fatalf("call fail: %v", r)
	}
	h, err := http.Get(ts.URL + "/mcp/health")
	if err != nil {
		t.Fatal(err)
	}
	var hm map[string]any
	_ = json.NewDecoder(h.Body).Decode(&hm)
	if hm["ok"] != true || hm["requests_total"].(float64) != 2 || hm["requests_failed"].(float64) != 1 {
		t.Fatalf("health: %v", hm)
	}
}

func freeBase(t *testing.T, span int) int {
	for b := 41000; b < 60000; b += span {
		ok := true
		for p := b; p < b+span; p++ {
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
			if err != nil {
				ok = false
				break
			}
			ln.Close()
		}
		if ok {
			return b
		}
	}
	t.Fatal("no free port range")
	return 0
}

// Two windows: the second takes the next port, finds the first as a peer, calls it, and takes the base port over
// when the first closes.
func TestWindows(t *testing.T) {
	drive := false
	base := freeBase(t, 4)
	a, b := testServer(&drive), testServer(&drive)
	pa, err := a.Listen(base, 4)
	if err != nil || pa != base || !a.Primary() {
		t.Fatalf("first: %d %v", pa, err)
	}
	pb, err := b.Listen(base, 4)
	if err != nil || pb != base+1 || b.Primary() {
		t.Fatalf("second: %d %v", pb, err)
	}
	// Peers skips its own process: both servers are this test process, so ask with a different pid view via health.
	res, err := CallPeer(context.Background(), pa, "echo", map[string]any{"text": "peer"})
	if err != nil || string(res) != `{"text":"peer"}` {
		t.Fatalf("CallPeer: %s %v", res, err)
	}
	a.Close()
	deadline := time.Now().Add(8 * time.Second)
	for !b.Primary() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !b.Primary() {
		t.Fatalf("second window did not take the base port over: %v", b.Ports())
	}
	res, err = CallPeer(context.Background(), base, "echo", map[string]any{"text": "taken"})
	if err != nil || string(res) != `{"text":"taken"}` {
		t.Fatalf("base after takeover: %s %v", res, err)
	}
	b.Close()
}
