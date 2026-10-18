package heroes

import "math"

// Expr is a STUConfigVarExpression / ExpressionTyped whose operands are plain var paths, float
// constants and literal config vars (herogen keeps only those). Bytecode and semantics:
// docs/StatescriptConfigVars.md §3.
type Expr struct {
	Ops    []byte
	Vars   []Set // m_dynamicVars: From / FromOwner
	Consts []float32
	CVs    []Value // m_configVars as literals
}

const exprEps = 1e-6

// Eval runs the expression over the start-up's instance and entity bags, as the client's
// interpreter would at that moment (a missing var reads FLT 0). false if it uses an opcode the
// start-up can't evaluate (vectors, other entities, value providers, dictionaries, random).
func (e *Expr) Eval(inst, owner []Var) (Value, bool) {
	var st []Value
	pop := func() Value { v := st[len(st)-1]; st = st[:len(st)-1]; return v }
	f := func(v Value) float64 {
		switch v.Type {
		case TypeFloat:
			return float64(v.Float())
		case TypeInt:
			return float64(int32(v.Bits))
		case TypeByte, TypeInt64:
			return float64(v.Bits)
		}
		return 0
	}
	flt := func(x float64) Value { return Float(float32(x)) }
	b := func(c bool) Value {
		if c {
			return Byte(1)
		}
		return Byte(0)
	}
	equal := func(l, r Value) bool {
		if l.Type == TypeInt64 || r.Type == TypeInt64 {
			return l.Type == r.Type && l.Bits == r.Bits
		}
		if l.Type == TypeString || r.Type == TypeString {
			return l.Type == r.Type && l.Str == r.Str
		}
		return math.Abs(f(l)-f(r)) <= exprEps
	}
	for pc := 0; pc < len(e.Ops); pc++ {
		op := e.Ops[pc]
		arg := func() (int, bool) {
			if pc+1 >= len(e.Ops) {
				return 0, false
			}
			return int(e.Ops[pc+1]), true
		}
		switch {
		case op == 0:
			if len(st) == 0 {
				return Float(0), true
			}
			return st[len(st)-1], true
		case op >= 1 && op <= 5, op == 30, op == 31, op == 70:
			a, ok := arg()
			if !ok || (op != 1 && op != 30 && op != 31 && op != 70 && len(st) == 0) {
				return Value{}, false
			}
			jump := false
			switch op {
			case 1:
				jump = true
			case 2: // and: falsy top decides (kept), else pop
				if !st[len(st)-1].Truthy() {
					jump = true
				} else {
					pop()
				}
			case 3: // or
				if st[len(st)-1].Truthy() {
					jump = true
				} else {
					pop()
				}
			case 4:
				jump = !pop().Truthy()
			case 5:
				jump = pop().Truthy()
			case 30:
				if a >= len(e.CVs) {
					return Value{}, false
				}
				st = append(st, e.CVs[a])
			case 31:
				if a >= len(e.Vars) {
					return Value{}, false
				}
				v, ok := e.Vars[a].apply(inst, owner)
				if !ok {
					v = Float(0)
				}
				st = append(st, v)
			case 70:
				if a >= len(e.Consts) {
					return Value{}, false
				}
				st = append(st, Float(e.Consts[a]))
			}
			if jump {
				pc += a - 1 // relative to the jump opcode; the loop adds 1
			} else {
				pc++ // the argument byte
			}
		case op >= 100 && op <= 105, op >= 140 && op <= 145, op == 167, op == 200, op == 201:
			if len(st) < 2 || op == 167 {
				return Value{}, false
			}
			r, l := pop(), pop()
			lf, rf := f(l), f(r)
			var v Value
			switch op {
			case 100:
				v = flt(lf + rf)
			case 101:
				v = flt(lf - rf)
			case 102:
				v = flt(lf * rf)
			case 103:
				if rf == 0 {
					v = flt(0)
				} else {
					v = flt(lf / rf)
				}
			case 104:
				if rf == 0 {
					v = flt(0)
				} else {
					v = flt(lf - math.Trunc(lf/rf)*rf)
				}
			case 105:
				if lf <= 0 {
					v = flt(0)
				} else {
					v = flt(math.Pow(lf, rf))
				}
			case 140:
				v = b(lf-exprEps <= rf)
			case 141:
				v = b(lf < rf-exprEps)
			case 142:
				v = b(lf+exprEps >= rf)
			case 143:
				v = b(lf > rf+exprEps)
			case 144:
				v = b(equal(l, r))
			case 145:
				v = b(!equal(l, r))
			case 200:
				v = flt(math.Min(lf, rf))
			case 201:
				v = flt(math.Max(lf, rf))
			}
			st = append(st, v)
		case op == 106, op == 120, op == 165, op == 166, op == 203, op == 204:
			if len(st) < 1 {
				return Value{}, false
			}
			x := pop()
			switch op {
			case 106:
				st = append(st, flt(-f(x)))
			case 120:
				st = append(st, b(!x.Truthy()))
			case 165, 166:
				st = append(st, flt(math.Abs(f(x))))
			case 203:
				st = append(st, flt(math.Floor(f(x))))
			case 204:
				st = append(st, flt(math.Ceil(f(x))))
			}
		default:
			return Value{}, false
		}
	}
	if len(st) == 0 {
		return Float(0), true
	}
	return st[len(st)-1], true
}
