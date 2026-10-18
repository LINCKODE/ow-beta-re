// Command shadowcmp compares the instance server's shadow simulation of the local hero with the client's own
// Statescript, state by state and var by var, at the same command frames (T052, docs/StatescriptSync.md §7).
//
// Setup: the instance server runs with --shadow (it simulates the hero and encodes its deltas but sends none, so
// the client predicts on its own and undoes each prediction 40 CF later); the client is in a match on it with a
// hero locked in; Prometheus' MCP server answers on :17888.
//
// Per action: wait until the client's predictions have been undone (the client is back at the lock-in state),
// re-seed the server from the start-up (POST /shadow/reset), compare once (baseline), press the button through
// Prometheus' logical_button (the real input path: the server gets the same input), capture the client's state
// (ss_serialize_local) repeatedly for a window of frames, then compare each capture with the server's snapshot of
// the same command frame (GET /shadow). The client can't be the reference for state data it never writes
// (docs/StatescriptSync.md §4.1): those types are compared by activity only.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	mcpURL     = flag.String("mcp", "http://127.0.0.1:17888/mcp/tools/call", "Prometheus MCP tool endpoint")
	controlURL = flag.String("control", "http://127.0.0.1:3729", "instance server control API")
	entityFlag = flag.String("entity", "", "hero body entity id (default: the simulated hero from /stats)")
	actions    = flag.String("actions", "fire,reload,ability_1,ability_2,jump", "logical buttons to press, one test each")
	holdMS     = flag.Int("hold", 100, "tap hold time (ms)")
	settle     = flag.Duration("settle", 3*time.Second, "wait before each test (the client undoes its predictions 40 CF after them)")
	window     = flag.Duration("window", 560*time.Millisecond, "capture window after the press (35 CF; the client undoes the press at +40)")
	root       = flag.String("root", "..", "new-stuff directory (for systems/data)")
	outFile    = flag.String("out", "", "write the full JSON report here")
	repeat     = flag.Int("repeat", 1, "run each action this many times")
	evidence   = flag.String("evidence", "", "write the per-state evidence (checks / mismatches per graph:state) here; instance --evidence reads it")
	gate       = flag.Bool("gate", false, "exit 1 if a state the server corrects (not frozen) shows a mismatch")
)

// ---- inputs -----------------------------------------------------------------------------------------------------

type heroGraph struct {
	Graph      string   `json:"graph"`
	States     string   `json:"states"`
	StateTypes []string `json:"state_types"`
}

type formatInfo struct {
	Hash      string `json:"hash"`
	Name      string `json:"name"`
	WriterOK  bool   `json:"writer_matches_reader"`
	HasUpdate bool   `json:"has_update"`
	Local     []struct {
		K    string `json:"k"`
		N    int    `json:"n"`
		Name string `json:"name"`
	} `json:"local"`
}

// serialBits is the width of a leading "serial" field (Anim u3, Effect u2): a counter the client's reader takes
// over from the server's data (Anim: G:0x10a4c80 overwrites the state serial, T054), so in shadow mode, where the
// client never receives our data, the two count independently and the field is not compared.
func (f formatInfo) serialBits() int {
	if len(f.Local) > 0 && f.Local[0].K == "u" && f.Local[0].Name == "serial" {
		return f.Local[0].N
	}
	return 0
}

var (
	graphs  = map[uint16]heroGraph{}
	formats = map[string]formatInfo{}
)

func loadData() error {
	var h struct {
		Graphs []heroGraph `json:"graphs"`
	}
	if err := readJSON(*root+"/systems/data/heroes/heroes.json", &h); err != nil {
		return err
	}
	for _, g := range h.Graphs {
		v, _ := strconv.ParseUint(g.Graph, 0, 64)
		graphs[uint16(v)] = g
	}
	var f struct {
		Types []formatInfo `json:"types"`
	}
	if err := readJSON(*root+"/systems/data/statescript/state_data_formats.json", &f); err != nil {
		return err
	}
	for _, t := range f.Types {
		formats[t.Hash] = t
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ---- the two sides ----------------------------------------------------------------------------------------------

type clientVar struct {
	ID, Type, Value string
}

type clientState struct {
	State int    `json:"state"`
	Bits  int    `json:"data_bits"`
	Hex   string `json:"data_hex"`
}

type clientInstance struct {
	Instance int           `json:"instance"`
	Graph    string        `json:"graph"`
	Active   []clientState `json:"active"`
	Vars     []clientVar   `json:"vars"`
	Events   []any         `json:"events"`
}

type clientSnap struct {
	ClientCF  uint32           `json:"client_cf"`
	Instances []clientInstance `json:"instances"`
	OwnerVars []clientVar      `json:"owner_vars"`
}

type serverVar struct {
	Type int    `json:"type"`
	Bits uint64 `json:"bits"`
}

type serverState struct {
	N      int    `json:"n"`
	Hex    string `json:"hex"`
	CapN   int    `json:"cap_n"`   // the same data at the capture clock (CF*16 + 15), when it differs
	CapHex string `json:"cap_hex"` //
}

type serverInstance struct {
	ID     uint16               `json:"id"`
	Graph  uint16               `json:"graph"`
	Vars   map[string]serverVar `json:"vars"`
	States map[string]serverState
	Events []any `json:"events"`
	Frozen []int `json:"frozen"` // states the server doesn't correct
}

type serverSnap struct {
	CF        uint32               `json:"cf"`
	Newest    uint32               `json:"newest"`
	Owner     map[string]serverVar `json:"owner"`
	Instances []serverInstance     `json:"instances"`
}

func mcp(tool string, args map[string]any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
	resp, err := http.Post(*mcpURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	// POST /mcp/tools/call answers {"result": <the tool's JSON>} (systems/ecs/tools/live.py).
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(b, &env) == nil && len(env.Result) > 0 {
		b = env.Result
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return nil, fmt.Errorf("%s: %s", tool, e.Error)
	}
	return b, nil
}

func captureClient(entity string) (*clientSnap, error) {
	raw, err := mcp("ss_serialize_local", map[string]any{"entity_id": entity})
	if err != nil {
		return nil, err
	}
	var s clientSnap
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("ss_serialize_local: %w (%.200s)", err, raw)
	}
	return &s, nil
}

func control(method, path string, out any) error {
	req, _ := http.NewRequest(method, *controlURL+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s %s: %s", method, path, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// The client's prediction records (docs/StatescriptSync.md §5.2): sync object = component 0x23 + 0x5C0; +0x90 the
// oldest frame record (its CF at +0x98), +0xA0 the count. A prediction timeout (§5.5) frees records, so the oldest
// record changing means the client rolled back and replayed; captures after that are on another timeline.
var compAddr uint64

func readAbs(addr uint64, n int) ([]byte, error) {
	raw, err := mcp("read_memory_abs", map[string]any{"addr": fmt.Sprintf("%#x", addr), "length": n})
	if err != nil {
		return nil, err
	}
	var r struct {
		Hex string `json:"hex"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.ReplaceAll(r.Hex, " ", ""))
}

type records struct {
	Head   uint64 `json:"head"`
	HeadCF uint32 `json:"head_cf"`
	Count  int32  `json:"count"`
}

func clientRecords(entity string) (records, error) {
	if compAddr == 0 {
		raw, err := mcp("read_component", map[string]any{"entity_id": entity, "component_id": "0x23", "length": 16})
		if err != nil {
			return records{}, err
		}
		var r struct {
			Addr string `json:"addr"`
		}
		json.Unmarshal(raw, &r)
		compAddr, _ = strconv.ParseUint(r.Addr, 0, 64)
	}
	b, err := readAbs(compAddr+0x5C0+0x90, 0x14)
	if err != nil || len(b) < 0x14 {
		return records{}, fmt.Errorf("records: %v", err)
	}
	rec := records{Head: binary.LittleEndian.Uint64(b), Count: int32(binary.LittleEndian.Uint32(b[0x10:]))}
	if rec.Head != 0 {
		if c, err := readAbs(rec.Head+0x98, 4); err == nil && len(c) == 4 {
			rec.HeadCF = binary.LittleEndian.Uint32(c)
		}
	}
	return rec, nil
}

// serverAt waits until the server has simulated frame cf (its input arrives a few frames after the client ran it).
func serverAt(body string, cf uint32) (*serverSnap, error) {
	var last error
	for i := 0; i < 100; i++ {
		var s serverSnap
		if last = control("GET", fmt.Sprintf("/shadow?body=%s&cf=%d", body, cf), &s); last == nil {
			return &s, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, last
}

// ---- comparison -------------------------------------------------------------------------------------------------

type mismatch struct {
	CF       uint32 `json:"cf"`
	Instance int    `json:"instance"`
	Graph    string `json:"graph,omitempty"`
	What     string `json:"what"` // "state", "data", "var"
	Key      string `json:"key"`
	Type     string `json:"type,omitempty"`
	Client   string `json:"client"`
	Server   string `json:"server"`
	Note     string `json:"note,omitempty"`
}

type typeStat struct {
	Name              string `json:"name"`
	ActivityChecks    int    `json:"activity_checks"`
	ActivityMismatch  int    `json:"activity_mismatches"`
	DataChecks        int    `json:"data_checks"`
	DataMismatch      int    `json:"data_mismatches"`
	DataNotComparable int    `json:"data_not_comparable"` // the client has no writer for this type
}

type result struct {
	Action     string               `json:"action"`
	Baseline   []mismatch           `json:"baseline_mismatches"`
	Captures   []uint32             `json:"captured_cfs"`
	Dropped    []uint32             `json:"dropped_cfs"` // captures after a client prediction timeout (another timeline)
	Records    []records            `json:"records"`
	Mismatches []mismatch           `json:"mismatches"`
	VarChecks  int                  `json:"var_checks"`
	Types      map[string]*typeStat `json:"types"`
	Error      string               `json:"error,omitempty"`
}

func clientValue(v clientVar) (float64, string, bool) {
	switch v.Type {
	case "CV_TYPE_INT":
		i, err := strconv.ParseInt(v.Value, 10, 64)
		return float64(i), "int", err == nil
	case "CV_TYPE_BYTE":
		if v.Value == "true" {
			return 1, "int", true
		}
		return 0, "int", v.Value == "false"
	case "CV_TYPE_FLT":
		f, err := strconv.ParseFloat(v.Value, 64)
		return f, "flt", err == nil
	case "CV_TYPE_INT64":
		u, err := strconv.ParseUint(v.Value, 16, 64)
		return float64(u), "i64", err == nil
	}
	return 0, "", false
}

func serverValue(v serverVar) (float64, string) {
	switch v.Type {
	case 1, 2: // BYTE, INT (the wire has no BYTE)
		return float64(int64(v.Bits)), "int"
	case 3:
		return float64(math.Float32frombits(uint32(v.Bits))), "flt"
	case 4:
		return float64(v.Bits), "i64"
	}
	return 0, ""
}

func compare(cf uint32, c *clientSnap, s *serverSnap, res *result, tally bool) []mismatch {
	var out []mismatch
	varsCmp := func(inst int, cv []clientVar, sv map[string]serverVar) {
		for _, v := range cv {
			cval, ck, ok := clientValue(v)
			if !ok {
				continue // ENTITY, SS_INSTANCEID, strings: not synced by the server
			}
			s, have := sv[v.ID]
			if !have {
				continue // a var the server doesn't hold (client code writes some; §6.3)
			}
			sval, sk := serverValue(s)
			if tally {
				res.VarChecks++
			}
			same := ck == sk && (ck != "flt" && cval == sval || ck == "flt" && math.Abs(cval-sval) <= 5e-5+1e-6*math.Abs(sval))
			if !same {
				out = append(out, mismatch{CF: cf, Instance: inst, What: "var", Key: "$" + v.ID,
					Client: fmt.Sprintf("%s %s", strings.TrimPrefix(v.Type, "CV_TYPE_"), v.Value), Server: fmt.Sprintf("%s %g", sk, sval)})
			}
		}
	}
	varsCmp(0, c.OwnerVars, s.Owner)
	sv := map[int]*serverInstance{}
	for i := range s.Instances {
		sv[int(s.Instances[i].ID)] = &s.Instances[i]
	}
	seen := map[int]bool{}
	for _, ci := range c.Instances {
		if ci.Instance <= 0 {
			continue // the client's own unsynchronized instances
		}
		seen[ci.Instance] = true
		si := sv[ci.Instance]
		if si == nil {
			out = append(out, mismatch{CF: cf, Instance: ci.Instance, What: "instance", Key: ci.Graph, Client: "present", Server: "absent"})
			continue
		}
		varsCmp(ci.Instance, ci.Vars, si.Vars)
		g, ok := graphs[si.Graph]
		if !ok {
			continue
		}
		cs := map[int]clientState{}
		for _, a := range ci.Active {
			cs[a.State] = a
		}
		for st := 0; st < len(g.StateTypes) && st < len(g.States); st++ {
			if g.States[st] == 'C' || g.States[st] == 'V' {
				continue // client-only / server-only states are never synced
			}
			typ := strings.TrimPrefix(g.StateTypes[st], "0x")
			fi := formats[typ]
			name := fi.Name
			if name == "" {
				name = typ
			}
			ts := res.Types[typ]
			if ts == nil {
				ts = &typeStat{Name: name}
				res.Types[typ] = ts
			}
			ca, con := cs[st]
			ss, son := si.States[strconv.Itoa(st)]
			ev := evidenceFor(si, st, typ, name)
			ev.Checks++
			if tally {
				ts.ActivityChecks++
			}
			if con != son {
				if tally {
					ts.ActivityMismatch++
				}
				ev.Mismatches++
				out = append(out, mismatch{CF: cf, Instance: ci.Instance, Graph: fmt.Sprintf("%#x", si.Graph), What: "state", Key: fmt.Sprintf("s%d", st), Type: name,
					Client: onOff(con), Server: onOff(son)})
				continue
			}
			if !con {
				continue
			}
			if !fi.WriterOK {
				if tally {
					ts.DataNotComparable++
				}
				continue
			}
			if tally {
				ts.DataChecks++
			}
			skip := fi.serialBits()
			same := ca.Bits == ss.N && sameBitsFrom(ca.Hex, ss.Hex, skip, ca.Bits)
			if !same && ss.CapHex != "" { // the client's writer runs at CF*16 + 15 (§7.1)
				same = ca.Bits == ss.CapN && sameBitsFrom(ca.Hex, ss.CapHex, skip, ca.Bits)
			}
			if !same {
				if tally {
					ts.DataMismatch++
				}
				ev.Mismatches++
				out = append(out, mismatch{CF: cf, Instance: ci.Instance, Graph: fmt.Sprintf("%#x", si.Graph), What: "data", Key: fmt.Sprintf("s%d", st), Type: name,
					Client: fmt.Sprintf("%d:%s", ca.Bits, ca.Hex), Server: fmt.Sprintf("%d:%s", ss.N, ss.Hex)})
			}
		}
	}
	for id, si := range sv {
		if !seen[id] {
			out = append(out, mismatch{CF: cf, Instance: id, What: "instance", Key: fmt.Sprintf("%#x", si.Graph), Client: "absent", Server: "present"})
		}
	}
	return out
}

// Evidence is the per-state result over a whole run (baseline and captures): the instance server's --evidence
// freezes the states with mismatches (docs/InstanceServer.md §10.3).
type Evidence struct {
	Source string                    `json:"source"`
	States map[string]*StateEvidence `json:"states"` // "0x<graph>:<m_states index>"
}

type StateEvidence struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Checks     int    `json:"checks"`
	Mismatches int    `json:"mismatches"`
	Frozen     bool   `json:"frozen"` // not corrected by the server in this run
}

var ev = Evidence{States: map[string]*StateEvidence{}}

func evidenceFor(si *serverInstance, st int, typ, name string) *StateEvidence {
	k := fmt.Sprintf("%#x:%d", si.Graph, st)
	e := ev.States[k]
	if e == nil {
		e = &StateEvidence{Type: typ, Name: name, Frozen: slices.Contains(si.Frozen, st)}
		ev.States[k] = e
	}
	return e
}

func onOff(b bool) string {
	if b {
		return "active"
	}
	return "inactive"
}

func sameBits(a, b string, n int) bool { return sameBitsFrom(a, b, 0, n) }

func sameBitsFrom(a, b string, from, n int) bool {
	x, _ := hex.DecodeString(a)
	y, _ := hex.DecodeString(b)
	for i := from; i < n; i++ {
		var p, q byte
		if i/8 < len(x) {
			p = x[i/8] >> (i % 8) & 1
		}
		if i/8 < len(y) {
			q = y[i/8] >> (i % 8) & 1
		}
		if p != q {
			return false
		}
	}
	return true
}

// ---- driver -----------------------------------------------------------------------------------------------------

func heroBody() (string, error) {
	var ms []struct {
		Sim []struct{ Body uint32 } `json:"sim"`
	}
	if err := control("GET", "/matches", &ms); err != nil {
		return "", err
	}
	for _, m := range ms {
		for _, h := range m.Sim {
			return fmt.Sprintf("%#x", h.Body), nil
		}
	}
	return "", fmt.Errorf("no simulated hero (is the instance server running with --shadow and a hero locked in?)")
}

func runAction(entity, button string) *result {
	res := &result{Action: button, Types: map[string]*typeStat{}}
	fail := func(err error) *result { res.Error = err.Error(); return res }
	time.Sleep(*settle)
	if err := control("POST", "/shadow/reset?body="+entity, nil); err != nil {
		return fail(err)
	}
	c0, err := captureClient(entity)
	if err != nil {
		return fail(err)
	}
	s0, err := serverAt(entity, c0.ClientCF)
	if err != nil {
		return fail(err)
	}
	res.Baseline = compare(c0.ClientCF, c0, s0, res, false)
	// Press right after a client prediction timeout (the records were just freed), for the longest clean window.
	r0, err := clientRecords(entity)
	if err != nil {
		return fail(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		r, err := clientRecords(entity)
		if err != nil {
			return fail(err)
		}
		if r.HeadCF != r0.HeadCF {
			break
		}
	}
	if _, err := mcp("logical_button", map[string]any{"action": "tap", "button": button, "hold_ms": *holdMS}); err != nil {
		return fail(err)
	}
	var caps []*clientSnap
	var ref *records
	end := time.Now().Add(*window)
	for time.Now().Before(end) {
		c, err := captureClient(entity)
		if err != nil {
			return fail(err)
		}
		rec, err := clientRecords(entity)
		if err != nil {
			return fail(err)
		}
		if len(caps) > 0 && c.ClientCF == caps[len(caps)-1].ClientCF {
			continue
		}
		res.Records = append(res.Records, rec)
		if ref == nil {
			ref = &rec
		} else if rec.HeadCF != ref.HeadCF { // pool pointers are reused: compare the oldest record's CF
			res.Dropped = append(res.Dropped, c.ClientCF)
			continue
		}
		caps = append(caps, c)
		time.Sleep(10 * time.Millisecond)
	}
	for _, c := range caps {
		s, err := serverAt(entity, c.ClientCF)
		if err != nil {
			return fail(err)
		}
		res.Captures = append(res.Captures, c.ClientCF)
		res.Mismatches = append(res.Mismatches, compare(c.ClientCF, c, s, res, true)...)
	}
	return res
}

func main() {
	flag.Parse()
	if err := loadData(); err != nil {
		fmt.Fprintln(os.Stderr, "data:", err)
		os.Exit(1)
	}
	entity := *entityFlag
	if entity == "" {
		var err error
		if entity, err = heroBody(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	var all []*result
	for _, a := range strings.Split(*actions, ",") {
		for rep := 0; rep < *repeat; rep++ {
			r := runAction(entity, strings.TrimSpace(a))
			all = append(all, r)
			report(r)
		}
	}
	if *outFile != "" {
		b, _ := json.MarshalIndent(all, "", " ")
		os.WriteFile(*outFile, b, 0o644)
	}
	ev.Source = fmt.Sprintf("shadowcmp %s, actions %s x%d, %s", entity, *actions, *repeat, time.Now().Format(time.RFC3339))
	if *evidence != "" {
		b, _ := json.MarshalIndent(ev, "", " ")
		if err := os.WriteFile(*evidence, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "evidence:", err)
			os.Exit(1)
		}
	}
	// The gate: every state the server corrects must have matched in every comparison.
	var bad []string
	for k, e := range ev.States {
		if e.Mismatches > 0 && !e.Frozen {
			bad = append(bad, fmt.Sprintf("%s %s (%d/%d)", k, e.Name, e.Mismatches, e.Checks))
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		fmt.Printf("gate: %d corrected states with mismatches:\n  %s\n", len(bad), strings.Join(bad, "\n  "))
		if *gate {
			os.Exit(1)
		}
	} else {
		fmt.Println("gate: every corrected state matched")
	}
}

func report(r *result) {
	fmt.Printf("== %s: %d captures (CF %v), %d after a client timeout", r.Action, len(r.Captures), span(r.Captures), len(r.Dropped))
	if r.Error != "" {
		fmt.Printf("  ERROR %s\n", r.Error)
		return
	}
	fmt.Printf(", baseline mismatches %d, mismatches %d, vars checked %d\n", len(r.Baseline), len(r.Mismatches), r.VarChecks)
	keys := make([]string, 0, len(r.Types))
	for k, t := range r.Types {
		if t.ActivityMismatch+t.DataMismatch > 0 || t.DataChecks+t.DataNotComparable > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return r.Types[keys[i]].Name < r.Types[keys[j]].Name })
	for _, k := range keys {
		t := r.Types[k]
		fmt.Printf("   %-40s activity %4d/%-4d  data %4d/%-4d  not comparable %d\n", t.Name,
			t.ActivityChecks-t.ActivityMismatch, t.ActivityChecks, t.DataChecks-t.DataMismatch, t.DataChecks, t.DataNotComparable)
	}
	for i, m := range append(r.Baseline, r.Mismatches...) {
		if i == 40 {
			fmt.Printf("   … %d more\n", len(r.Baseline)+len(r.Mismatches)-40)
			break
		}
		tag := ""
		if i < len(r.Baseline) {
			tag = "(baseline) "
		}
		fmt.Printf("   %scf %d i%d %s %s %s: client %s, server %s\n", tag, m.CF, m.Instance, m.What, m.Key, m.Type, m.Client, m.Server)
	}
}

func span(c []uint32) string {
	if len(c) == 0 {
		return "-"
	}
	return fmt.Sprintf("%d..%d", c[0], c[len(c)-1])
}
