package lobby

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// LuaVM is a persistent Lua state inside the gateway, for live experiments.
// Globals and handlers survive between evals until Reset.
//
// API (see Help):
//
//	sessions()                          connected clients
//	send(protocol|nil, msg, payload [, session])  payload is a (binary) string
//	on(key, fn)  off(key)  handlers()   key = message ID, protocol name or "*";
//	                                    fn(m) gets {session, protocol, msg, payload};
//	                                    returning true stops the message reaching lobbylogic
//	recent([n [, session]])             last n traffic entries
//	protocols()  protocol(name)         protocol list / one protocol with the dump's field tables
//	pack(fmt, ...)  unpack(fmt, s [, pos])  binary encoding, see packHelp
//	hex(s)  unhex(h)  print(...)  log(...)
type LuaVM struct {
	log      *slog.Logger
	hub      *Hub
	initPath string
	dump     map[uint32]any // protocol CRC -> decoded jam_protocols.json entry

	mu       sync.Mutex
	L        *lua.LState
	handlers map[string]*lua.LFunction
	out      *strings.Builder // print() target during an eval; nil = log
}

const evalTimeout = 5 * time.Second

func NewLuaVM(log *slog.Logger, hub *Hub, initPath string, dump map[uint32]any) *LuaVM {
	vm := &LuaVM{log: log, hub: hub, initPath: initPath, dump: dump}
	hub.lua = vm
	vm.Reset()
	return vm
}

// Reset discards all Lua state and runs the init script again.
func (vm *LuaVM) Reset() string {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.L != nil {
		vm.L.Close()
	}
	// Only the side-effect-free libraries: no os/io/package, so an eval can't
	// touch the filesystem or run programs.
	vm.L = lua.NewState(lua.Options{SkipOpenLibs: true})
	for _, lib := range []struct {
		name string
		fn   lua.LGFunction
	}{{lua.BaseLibName, lua.OpenBase}, {lua.TabLibName, lua.OpenTable}, {lua.StringLibName, lua.OpenString}, {lua.MathLibName, lua.OpenMath}} {
		vm.L.Push(vm.L.NewFunction(lib.fn))
		vm.L.Push(lua.LString(lib.name))
		vm.L.Call(1, 0)
	}
	vm.L.SetGlobal("dofile", lua.LNil)
	vm.L.SetGlobal("loadfile", lua.LNil)
	vm.handlers = map[string]*lua.LFunction{}
	vm.register()
	if vm.initPath == "" {
		return "reset"
	}
	if _, err := os.Stat(vm.initPath); err != nil {
		vm.log.Info("lua: no init script", "path", vm.initPath)
		return "reset (no init script)"
	}
	var b strings.Builder
	vm.out = &b
	defer func() { vm.out = nil }()
	ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	vm.L.SetContext(ctx)
	defer vm.L.RemoveContext()
	if err := vm.L.DoFile(vm.initPath); err != nil {
		vm.log.Error("lua: init script failed", "path", vm.initPath, "err", err)
		return "reset; init script failed: " + err.Error()
	}
	vm.log.Info("lua: loaded init script", "path", vm.initPath)
	return "reset; loaded " + vm.initPath + "\n" + b.String()
}

// Eval runs src and returns what it printed plus its results. A bare
// expression works too ("sessions()" is evaluated as "return sessions()").
func (vm *LuaVM) Eval(src string) (string, error) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	fn, err := vm.L.LoadString("return " + src)
	if err != nil {
		if fn, err = vm.L.LoadString(src); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	vm.out = &b
	defer func() { vm.out = nil }()
	ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	vm.L.SetContext(ctx)
	defer vm.L.RemoveContext()

	top := vm.L.GetTop()
	vm.L.Push(fn)
	if err := vm.L.PCall(0, lua.MultRet, nil); err != nil {
		return b.String(), err
	}
	n := vm.L.GetTop() - top
	for i := 1; i <= n; i++ {
		b.WriteString("=> ")
		b.WriteString(formatLua(vm.L.Get(top+i), 0))
		b.WriteByte('\n')
	}
	vm.L.Pop(n)
	return b.String(), nil
}

// handle runs the Lua handlers for a client message; true = consumed.
func (vm *LuaVM) handle(session uint64, protocol string, msgID uint16, payload []byte) bool {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	consumed := false
	for _, key := range []string{strconv.Itoa(int(msgID)), protocol, "*"} {
		fn := vm.handlers[key]
		if fn == nil {
			continue
		}
		m := vm.L.NewTable()
		m.RawSetString("session", lua.LNumber(session))
		m.RawSetString("protocol", lua.LString(protocol))
		m.RawSetString("msg", lua.LNumber(msgID))
		m.RawSetString("payload", lua.LString(payload))
		ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
		vm.L.SetContext(ctx)
		err := vm.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, m)
		vm.L.RemoveContext()
		cancel()
		if err != nil {
			vm.log.Error("lua handler failed", "key", key, "msg", msgID, "err", err)
			continue
		}
		if lua.LVAsBool(vm.L.Get(-1)) {
			consumed = true
		}
		vm.L.Pop(1)
	}
	if consumed {
		vm.log.Info("lua handler consumed message", "msg", msgID)
	}
	return consumed
}

func (vm *LuaVM) register() {
	L := vm.L
	fns := map[string]lua.LGFunction{
		"print":     vm.luaPrint,
		"log":       vm.luaPrint,
		"sessions":  vm.luaSessions,
		"send":      vm.luaSend,
		"inject":    vm.luaInject,
		"on":        vm.luaOn,
		"off":       vm.luaOff,
		"handlers":  vm.luaHandlers,
		"recent":    vm.luaRecent,
		"protocols": vm.luaProtocols,
		"protocol":  vm.luaProtocol,
		"pack":      luaPack,
		"unpack":    luaUnpack,
		"hex":       func(L *lua.LState) int { L.Push(lua.LString(hex.EncodeToString([]byte(L.CheckString(1))))); return 1 },
		"unhex":     luaUnhex,
		"disconnect": func(L *lua.LState) int {
			if err := vm.hub.Disconnect(uint64(L.OptInt64(1, 0))); err != nil {
				L.RaiseError("%v", err)
			}
			return 0
		},
		"help": func(L *lua.LState) int { L.Push(lua.LString(LuaHelp)); return 1 },
	}
	for name, fn := range fns {
		L.SetGlobal(name, L.NewFunction(fn))
	}
}

func (vm *LuaVM) luaPrint(L *lua.LState) int {
	parts := make([]string, L.GetTop())
	for i := range parts {
		v := L.Get(i + 1)
		if s, ok := v.(lua.LString); ok {
			parts[i] = string(s)
		} else {
			parts[i] = formatLua(v, 0)
		}
	}
	line := strings.Join(parts, "\t")
	if vm.out != nil {
		vm.out.WriteString(line + "\n")
	} else {
		vm.log.Info("lua: " + line)
	}
	return 0
}

func (vm *LuaVM) luaSessions(L *lua.LState) int {
	L.Push(toLua(L, vm.hub.Sessions()))
	return 1
}

// send(protocol|nil, msg, payload [, session])
func (vm *LuaVM) luaSend(L *lua.LState) int {
	protocol := ""
	if s, ok := L.Get(1).(lua.LString); ok {
		protocol = string(s)
	}
	msg := uint16(L.CheckInt(2))
	payload := []byte(L.OptString(3, ""))
	session := uint64(L.OptInt64(4, 0))
	if err := vm.hub.Send(session, protocol, msg, payload, "lua"); err != nil {
		L.RaiseError("%v", err)
	}
	return 0
}

func (vm *LuaVM) luaInject(L *lua.LState) int {
	msg := uint16(L.CheckInt(1))
	payload := []byte(L.OptString(2, ""))
	session := uint64(L.OptInt64(3, 0))
	if err := vm.hub.Inject(session, msg, payload); err != nil {
		L.RaiseError("%v", err)
	}
	return 0
}

func handlerKey(L *lua.LState) string {
	switch v := L.Get(1).(type) {
	case lua.LNumber:
		return strconv.Itoa(int(v))
	case lua.LString:
		return string(v)
	}
	L.ArgError(1, "message ID, protocol name or \"*\" expected")
	return ""
}

func (vm *LuaVM) luaOn(L *lua.LState) int {
	vm.handlers[handlerKey(L)] = L.CheckFunction(2)
	return 0
}

func (vm *LuaVM) luaOff(L *lua.LState) int {
	delete(vm.handlers, handlerKey(L))
	return 0
}

func (vm *LuaVM) luaHandlers(L *lua.LState) int {
	keys := make([]string, 0, len(vm.handlers))
	for k := range vm.handlers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t := L.NewTable()
	for _, k := range keys {
		t.Append(lua.LString(k))
	}
	L.Push(t)
	return 1
}

// recent([n [, session]]): payloads are hex strings.
func (vm *LuaVM) luaRecent(L *lua.LState) int {
	entries := vm.hub.Traffic(0, L.OptInt(1, 20), uint64(L.OptInt64(2, 0)))
	L.Push(toLua(L, entries))
	return 1
}

func (vm *LuaVM) luaProtocols(L *lua.LState) int {
	t := L.NewTable()
	for _, p := range Protocols() {
		e := L.NewTable()
		e.RawSetString("name", lua.LString(p.Name))
		e.RawSetString("crc", lua.LString(fmt.Sprintf("0x%08x", p.CRC)))
		e.RawSetString("first", lua.LNumber(p.Base))
		e.RawSetString("last", lua.LNumber(p.Max-1))
		e.RawSetString("server_type", lua.LNumber(p.ServerType))
		t.Append(e)
	}
	L.Push(t)
	return 1
}

func (vm *LuaVM) luaProtocol(L *lua.LState) int {
	p := ProtocolByName(L.CheckString(1))
	if p == nil {
		L.RaiseError("no protocol %q", L.CheckString(1))
	}
	L.Push(toLua(L, ProtocolDetail(p, vm.dump)))
	return 1
}

// toLua converts a Go value to Lua through its JSON form.
func toLua(L *lua.LState, v any) lua.LValue {
	b, err := json.Marshal(v)
	if err != nil {
		return lua.LString(err.Error())
	}
	var j any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.Decode(&j)
	return jsonToLua(L, j)
}

func jsonToLua(L *lua.LState, j any) lua.LValue {
	switch v := j.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(v)
	case json.Number:
		// Integers beyond 2^53 (IDs) don't fit a Lua number; keep them exact as strings.
		if i, err := strconv.ParseInt(string(v), 10, 64); err == nil && (i > 1<<53 || i < -(1<<53)) {
			return lua.LString(v)
		}
		if u, err := strconv.ParseUint(string(v), 10, 64); err == nil && u > 1<<53 {
			return lua.LString(v)
		}
		f, _ := v.Float64()
		return lua.LNumber(f)
	case string:
		return lua.LString(v)
	case []any:
		t := L.NewTable()
		for _, e := range v {
			t.Append(jsonToLua(L, e))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, e := range v {
			t.RawSetString(k, jsonToLua(L, e))
		}
		return t
	}
	return lua.LString(fmt.Sprint(j))
}

// formatLua renders a value for eval output; binary strings are shown as hex.
func formatLua(v lua.LValue, depth int) string {
	switch x := v.(type) {
	case lua.LString:
		s := string(x)
		for _, c := range []byte(s) {
			if c < 0x20 && c != '\n' && c != '\t' || c >= 0x7f {
				return "hex(" + hex.EncodeToString([]byte(s)) + ")"
			}
		}
		return strconv.Quote(s)
	case *lua.LTable:
		if depth > 6 {
			return "{...}"
		}
		indent := strings.Repeat("  ", depth+1)
		var items []string
		n := x.Len()
		for i := 1; i <= n; i++ {
			items = append(items, indent+formatLua(x.RawGetInt(i), depth+1))
		}
		var keys []string
		kv := map[string]lua.LValue{}
		x.ForEach(func(k, val lua.LValue) {
			if kn, ok := k.(lua.LNumber); ok && float64(kn) == math.Trunc(float64(kn)) && int(kn) >= 1 && int(kn) <= n {
				return
			}
			ks := k.String()
			keys = append(keys, ks)
			kv[ks] = val
		})
		sort.Strings(keys)
		for _, k := range keys {
			items = append(items, indent+k+" = "+formatLua(kv[k], depth+1))
		}
		if len(items) == 0 {
			return "{}"
		}
		return "{\n" + strings.Join(items, ",\n") + "\n" + strings.Repeat("  ", depth) + "}"
	case lua.LNumber:
		f := float64(x)
		if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
			return strconv.FormatInt(int64(f), 10)
		}
		return x.String()
	}
	return v.String()
}

const packHelp = `pack/unpack formats (little-endian, whitespace ignored):
  b u8   h u16   i u32   q u64   ? bool (1 byte)   f float32   d float64
  z  null-terminated string        s  [u32 length] + bytes
  cN fixed N bytes (pack pads/truncates)   xN N zero bytes (unpack skips)
  u64 values may be given as strings ("0x0240000000000096"); unpack returns
  u64 values above 2^53 as "0x..." strings.`

func luaPack(L *lua.LState) int {
	format := L.CheckString(1)
	var out []byte
	arg := 2
	next := func() lua.LValue { v := L.Get(arg); arg++; return v }
	err := walkFormat(format, func(op byte, n int) error {
		if op == 'x' {
			out = append(out, make([]byte, n)...)
			return nil
		}
		v := next()
		switch op {
		case 'b', 'h', 'i', 'q':
			u, err := luaUint(v)
			if err != nil {
				return fmt.Errorf("argument %d: %w", arg-1, err)
			}
			switch op {
			case 'b':
				out = append(out, byte(u))
			case 'h':
				out = binary.LittleEndian.AppendUint16(out, uint16(u))
			case 'i':
				out = binary.LittleEndian.AppendUint32(out, uint32(u))
			case 'q':
				out = binary.LittleEndian.AppendUint64(out, u)
			}
		case '?':
			if lua.LVAsBool(v) {
				out = append(out, 1)
			} else {
				out = append(out, 0)
			}
		case 'f':
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(lua.LVAsNumber(v))))
		case 'd':
			out = binary.LittleEndian.AppendUint64(out, math.Float64bits(float64(lua.LVAsNumber(v))))
		case 'z':
			out = append(append(out, lua.LVAsString(v)...), 0)
		case 's':
			s := lua.LVAsString(v)
			out = binary.LittleEndian.AppendUint32(out, uint32(len(s)))
			out = append(out, s...)
		case 'c':
			b := make([]byte, n)
			copy(b, lua.LVAsString(v))
			out = append(out, b...)
		}
		return nil
	})
	if err != nil {
		L.RaiseError("pack: %v", err)
	}
	L.Push(lua.LString(out))
	return 1
}

// unpack(fmt, s [, pos]) returns the values, then the position after them.
func luaUnpack(L *lua.LState) int {
	format, data := L.CheckString(1), []byte(L.CheckString(2))
	pos := L.OptInt(3, 1) - 1
	var vals []lua.LValue
	need := func(n int) error {
		if pos+n > len(data) {
			return fmt.Errorf("need %d bytes at position %d, have %d", n, pos+1, len(data)-pos)
		}
		return nil
	}
	err := walkFormat(format, func(op byte, n int) error {
		size := map[byte]int{'b': 1, '?': 1, 'h': 2, 'i': 4, 'f': 4, 'q': 8, 'd': 8, 'c': n, 'x': n}[op]
		switch op {
		case 'z':
			end := strings.IndexByte(string(data[pos:]), 0)
			if end < 0 {
				return errors.New("unterminated string")
			}
			vals = append(vals, lua.LString(data[pos:pos+end]))
			pos += end + 1
			return nil
		case 's':
			if err := need(4); err != nil {
				return err
			}
			l := int(binary.LittleEndian.Uint32(data[pos:]))
			pos += 4
			if err := need(l); err != nil {
				return err
			}
			vals = append(vals, lua.LString(data[pos:pos+l]))
			pos += l
			return nil
		}
		if err := need(size); err != nil {
			return err
		}
		b := data[pos : pos+size]
		pos += size
		switch op {
		case 'b':
			vals = append(vals, lua.LNumber(b[0]))
		case '?':
			vals = append(vals, lua.LBool(b[0] != 0))
		case 'h':
			vals = append(vals, lua.LNumber(binary.LittleEndian.Uint16(b)))
		case 'i':
			vals = append(vals, lua.LNumber(binary.LittleEndian.Uint32(b)))
		case 'q':
			u := binary.LittleEndian.Uint64(b)
			if u <= 1<<53 {
				vals = append(vals, lua.LNumber(u))
			} else {
				vals = append(vals, lua.LString(fmt.Sprintf("0x%016x", u)))
			}
		case 'f':
			vals = append(vals, lua.LNumber(math.Float32frombits(binary.LittleEndian.Uint32(b))))
		case 'd':
			vals = append(vals, lua.LNumber(math.Float64frombits(binary.LittleEndian.Uint64(b))))
		case 'c':
			vals = append(vals, lua.LString(b))
		}
		return nil
	})
	if err != nil {
		L.RaiseError("unpack: %v", err)
	}
	for _, v := range vals {
		L.Push(v)
	}
	L.Push(lua.LNumber(pos + 1))
	return len(vals) + 1
}

func walkFormat(format string, fn func(op byte, n int) error) error {
	for i := 0; i < len(format); i++ {
		op := format[i]
		switch op {
		case ' ', '\t', '\n', '<':
			continue
		case 'b', 'h', 'i', 'q', '?', 'f', 'd', 'z', 's':
			if err := fn(op, 0); err != nil {
				return err
			}
		case 'c', 'x':
			j := i + 1
			for j < len(format) && format[j] >= '0' && format[j] <= '9' {
				j++
			}
			n := 1
			if j > i+1 {
				n, _ = strconv.Atoi(format[i+1 : j])
			}
			if err := fn(op, n); err != nil {
				return err
			}
			i = j - 1
		default:
			return fmt.Errorf("unknown format character %q", op)
		}
	}
	return nil
}

func luaUint(v lua.LValue) (uint64, error) {
	switch x := v.(type) {
	case lua.LNumber:
		if x < 0 {
			return uint64(int64(x)), nil
		}
		return uint64(x), nil
	case lua.LString:
		if u, err := strconv.ParseUint(string(x), 0, 64); err == nil {
			return u, nil
		}
		i, err := strconv.ParseInt(string(x), 0, 64)
		return uint64(i), err
	case lua.LBool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("number expected, got %s", v.Type())
}

func luaUnhex(L *lua.LState) int {
	s := strings.Map(func(r rune) rune {
		if r == ' ' || r == ':' || r == '\n' || r == '\t' {
			return -1
		}
		return r
	}, L.CheckString(1))
	b, err := hex.DecodeString(s)
	if err != nil {
		L.RaiseError("unhex: %v", err)
	}
	L.Push(lua.LString(b))
	return 1
}

const LuaHelp = `Lua API (the state persists between evals until POST /lua/reset):
  sessions()                               connected clients (id, cid, JamIDs, channels)
  send(protocol|nil, msg, payload [, session])
                                           send to the client; nil protocol = look up by msg ID;
                                           session 0/nil = newest negotiated session
  inject(msg, payload [, session])         hand a message to lobbylogic as if the client sent it
                                           (skips Lua handlers); e.g. command("instance")
  on(key, fn) / off(key) / handlers()      key = message ID, protocol name or "*";
                                           fn(m) with m = {session, protocol, msg, payload};
                                           return true to keep the message from lobbylogic
  recent([n [, session]])                  last n traffic entries (payload as hex)
  protocols() / protocol(name)             protocol list / one protocol with its dump field tables
  pack(fmt, ...) / unpack(fmt, s [, pos])  binary encoding (see below); unpack also returns the next pos
  hex(s) / unhex(h)                        hex conversion (unhex ignores spaces and colons)
  disconnect([session])                    drop a client
  print(...) / log(...)                    output (to the eval result, or the gateway log in handlers)
` + packHelp
