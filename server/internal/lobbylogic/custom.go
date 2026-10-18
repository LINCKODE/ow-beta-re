package lobbylogic

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// PrometheusLobby: our own JAM protocol on the lobby link, handled by the
// Prometheus DLL in the client (docs/JAM.md §13). The server calls a Prometheus
// tool (any MCP tool, or notify / ping / tools) and gets its JSON result back.
const (
	protoPrometheus    = "PrometheusLobby"
	msgPrometheusCall  = 32000 // {u32 seq, str tool, str args}
	msgPrometheusReply = 32001 // {u32 seq, bool ok, str result}
)

var prometheusSeq atomic.Uint32

func encodePrometheusCall(seq uint32, tool, args string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, seq)
	b = append(b, tool...)
	b = append(b, 0)
	b = append(b, args...)
	return append(b, 0)
}

type prometheusResult struct {
	Seq    uint32
	OK     bool
	Result string
}

func decodePrometheusResult(p []byte) (prometheusResult, error) {
	var r prometheusResult
	if len(p) < 6 {
		return r, fmt.Errorf("PrometheusResult too short (%d bytes)", len(p))
	}
	r.Seq = binary.LittleEndian.Uint32(p)
	r.OK = p[4]&1 != 0
	s := p[5:]
	n := bytes.IndexByte(s, 0)
	if n < 0 || n != len(s)-1 {
		return r, fmt.Errorf("PrometheusResult: bad result string (%d bytes)", len(s))
	}
	r.Result = string(s[:n])
	return r, nil
}

// PrometheusCall asks the client's Prometheus to run a tool; args is a JSON object
// (nil = {}). The answer arrives as PrometheusResult with the returned seq.
func (c *Ctx) PrometheusCall(tool string, args any) (uint32, error) {
	text := "{}"
	switch a := args.(type) {
	case nil:
	case string:
		text = a
	default:
		b, err := json.Marshal(a)
		if err != nil {
			return 0, err
		}
		text = string(b)
	}
	if !json.Valid([]byte(text)) {
		return 0, fmt.Errorf("args are not valid JSON: %s", text)
	}
	seq := prometheusSeq.Add(1)
	if err := c.Send(protoPrometheus, msgPrometheusCall, encodePrometheusCall(seq, tool, text)); err != nil {
		return 0, fmt.Errorf("%w (the client needs Prometheus with CustomJam, and the gateway must know %s)", err, protoPrometheus)
	}
	c.Log.Info("prometheus call", "seq", seq, "tool", tool, "args", text)
	return seq, nil
}

// onPrometheusResult logs a PrometheusResult and echoes it as a system chat line.
func onPrometheusResult(c *Ctx) error {
	r, err := decodePrometheusResult(c.Payload)
	if err != nil {
		return err
	}
	c.Log.Info("prometheus result", "seq", r.Seq, "ok", r.OK, "result", r.Result)
	if handled, err := onServerViewHello(c, r); handled {
		return err
	}
	text := r.Result
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	status := "ok"
	if !r.OK {
		status = "error"
	}
	return c.SystemChat(fmt.Sprintf("[prometheus #%d %s] %s", r.Seq, status, text))
}

func init() {
	commands["custom"] = command{`!custom <tool> [json args] - run a Prometheus tool in the client over the PrometheusLobby JAM protocol (e.g. !custom notify {"text":"hi"}; !custom tools)`, func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return fmt.Errorf("usage: !custom <tool> [json args]")
		}
		var args any
		if len(a) > 1 {
			args = strings.Join(a[1:], " ")
		}
		seq, err := c.PrometheusCall(a[0], args)
		if err != nil {
			return err
		}
		return c.SystemChat(fmt.Sprintf("sent PrometheusCall #%d %s", seq, a[0]))
	}}
}
