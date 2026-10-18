// Built-in config-var evaluators: literals, identifiers/GUIDs, variable paths, the owner, and both expression
// interpreters (docs/StatescriptConfigVars.md §3; Ghidra 0xf6adc0 float, 0xf6c7b0 typed).
#include <algorithm>
#include <cmath>
#include <cstring>
#include <format>

#include "statescript/runtime.h"
#include "states_hero.h"
#include "match.h"

namespace ss {

namespace {

constexpr uint32_t kGetOwner = 0x261676f0, kDynamic = 0xf8cc195d, kExpression = 0xcc3a08f4, kExpressionTyped = 0xec502a81;
constexpr uint32_t kExpr = Hash("m_expression"), kOpcodes = Hash("m_opcodes"),
                   kDynamicVars = Hash("m_dynamicVars"), kConstants = 0x1b183605, kConfigVars = Hash("m_configVars");

enum class LiteralKind : uint8_t { Guid, IntField, False, True, Twenty, Byte, Float, Color, String, Vec3, Vec2 };
#include "literal_config_vars.h"

// The decoded field at `offset` of a config var (literal types keep their value at +0x10).
const stu::Value* FieldAt(const Graph& g, int cv, uint32_t offset) {
    const stu::TypeInfo* t = g.registry().Find(g.type_of(cv));
    if (!t) return nullptr;
    for (const stu::FieldInfo* f : g.registry().AllFields(*t))
        if (f->offset == offset) return g.value(cv).Find(f->hash);
    return nullptr;
}

float F32At(const stu::Value* v, size_t i) {
    float f = 0;
    if (v && v->raw.size() >= (i + 1) * 4) std::memcpy(&f, v->raw.data() + i * 4, 4);
    return f;
}

// Literal config vars (T038 catalogue, `reads: literal`): the value is a field of the STU object [G].
class LiteralEvaluator : public Evaluator {
public:
    explicit LiteralEvaluator(LiteralKind k) : k_(k) {}
    bool Get(const Graph& g, int cv, EvalContext&, Primitive& out) const override {
        switch (k_) {
        case LiteralKind::False: out = Primitive::Byte(false); return true;
        case LiteralKind::True: out = Primitive::Byte(true); return true;
        case LiteralKind::Twenty: out = Primitive::Float(20.0f); return true;
        default: break;
        }
        const stu::Value* v = FieldAt(g, cv, 0x10);
        switch (k_) {
        case LiteralKind::Guid: out = Primitive::Int64(Graph::RefGuid(v)); return true;
        case LiteralKind::IntField: out = Primitive::Int(v ? v->AsI32() : 0); return true;
        case LiteralKind::Byte: out = Primitive::Byte(v && v->AsU8() != 0); return true;
        case LiteralKind::Float: out = Primitive::Float(v ? v->AsF32() : 0.0f); return true;
        case LiteralKind::String: out = Primitive::String(v ? v->str : std::string()); return true;
        case LiteralKind::Vec3:
        case LiteralKind::Vec2: {
            float xyz[3] = {};
            int n = k_ == LiteralKind::Vec3 ? 3 : 2;
            if (v && v->raw.size() >= size_t(n) * 4) {
                for (int i = 0; i < n; i++) xyz[i] = F32At(v, i);
            } else {  // separate f32 fields
                for (int i = 0; i < n; i++) xyz[i] = FieldAt(g, cv, 0x10 + 4 * i) ? FieldAt(g, cv, 0x10 + 4 * i)->AsF32() : 0;
            }
            out = Primitive::Vec3(xyz[0], xyz[1], xyz[2]);
            return true;
        }
        case LiteralKind::Color: {  // each float channel x255 truncated, packed R,G,B,A low to high
            uint32_t packed = 0;
            for (int i = 0; i < 4; i++) packed |= uint32_t(uint8_t(int(F32At(v, i) * 255.0f))) << (8 * i);
            out = Primitive::Make(PrimType::ColorRGBA, packed);
            return true;
        }
        default: return false;
        }
    }

private:
    LiteralKind k_;
};

class OwnerEvaluator : public Evaluator {
public:
    bool Get(const Graph&, int, EvalContext& ctx, Primitive& out) const override {
        if (!ctx.instance) return false;
        out = Primitive::Entity(ctx.instance->sm().owner());
        return true;
    }
};

class DynamicEvaluator : public Evaluator {
public:
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override {
        if (!ctx.instance) return false;
        auto path = g.DynamicPath(cv);
        return !path.empty() && ctx.instance->GetVar(path, out);
    }
    // 0xf5c160: the var currently belongs to a state, or the path crosses into another bag / entity.
    bool DependsOnExternal(const Graph& g, int cv, EvalContext& ctx) const override {
        auto path = g.DynamicPath(cv);
        return ctx.instance && !path.empty() && ctx.instance->PathDependsOnExternal(path);
    }
};

struct ExprData {
    std::vector<uint8_t> ops;
    std::vector<int> dyn;     // Dynamic config var instances
    std::vector<float> consts;
    std::vector<int> cvs;     // m_configVars instances
};

ExprData ReadExpr(const Graph& g, int cv) {
    ExprData d;
    const stu::Value& obj = g.value(cv);
    if (const stu::Value* e = obj.Find(kExpr)) {
        if (auto* o = e->Find(kOpcodes))
            for (auto& it : o->items) d.ops.push_back(it.AsU8());
        if (auto* c = e->Find(kConstants))
            for (auto& it : c->items) d.consts.push_back(it.AsF32());
        d.dyn = g.Objects(*e, kDynamicVars);
    }
    d.cvs = g.Objects(obj, kConfigVars);
    return d;
}

// Opcodes with a 1-byte argument (docs/StatescriptConfigVars.md §3.4).
bool HasArg(uint8_t op) { return (op >= 1 && op <= 5) || (op >= 30 && op <= 34) || (op >= 36 && op <= 40) || op == 70; }

// Expression +0xD8 (0xf6beb0): op 35 in the bytecode, or any operand that depends on external state.
bool ExprDependsOnExternal(const Graph& g, int cv, EvalContext& ctx) {
    ExprData d = ReadExpr(g, cv);
    for (size_t pc = 0; pc < d.ops.size() && d.ops[pc] != 0; pc += HasArg(d.ops[pc]) ? 2 : 1)
        if (d.ops[pc] == 35) return true;
    for (int c : d.cvs)
        if (DependsOnExternal(ctx, c)) return true;
    for (int c : d.dyn)
        if (DependsOnExternal(ctx, c)) return true;
    return false;
}

void Unmodelled(EvalContext& ctx, uint32_t op, bool typed) {
    if (ctx.instance)
        ctx.instance->sm().warnings.push_back(std::format("expression opcode {} not modelled ({})", op,
                                                          typed ? "typed" : "float"));
}

float Round(float x) { return float(int32_t(x >= 0.0f ? x + 0.5f : x - 0.5f)); }

// Ops 32–34 (instance +0xF0 = 0x107efa0, then the lookup of +0xD8) [G]: the Dictionary var of Dynamic operand
// `dyn` (in `entity`'s entity bag after op 35); key BYTE / INT / FLT = position (FLT rounded half away from zero),
// else the first entry whose key equals it (loose ==). Found: its index; not found: −1.
int DictLookup(const Graph& g, int dyn, EvalContext& ctx, uint32_t entity, const Primitive& key, const Var** dict) {
    *dict = nullptr;
    if (dyn < 0 || !ctx.instance) return -1;
    auto path = g.DynamicPath(dyn);
    const Var* v = IsInvalidEntity(entity) ? ctx.instance->FindDictionary(path)
                                           : ctx.instance->sm().FindEntityDictionary(entity, path);
    *dict = v;
    if (!v) return -1;
    const auto& e = v->dict();
    if (key.type == PrimType::Byte || key.type == PrimType::Int || key.type == PrimType::Float) {
        int i = key.type == PrimType::Float ? int(Round(key.AsFloat())) : key.type == PrimType::Byte ? int(key.bits & 0xFF) : key.AsInt();
        return i >= 0 && i < int(e.size()) ? i : -1;
    }
    for (size_t i = 0; i < e.size(); i++)
        if (LooselyEqual(e[i].first, key)) return int(i);
    return -1;
}

// A primitive on the float interpreter's stack (0xf6adc0): BYTE 1 / 0, INT and FLT as floats, anything else 0.
float NumFloat(const Primitive& p) {
    switch (p.type) {
    case PrimType::Byte: return (p.bits & 0xFF) ? 1.0f : 0.0f;
    case PrimType::Int: return float(p.AsInt());
    case PrimType::Float: return p.AsFloat();
    default: return 0.0f;
    }
}

// Op 36 (instance +0xC8 = 0xf4d580) [G]: the sync manager's value providers answer for the last id of a
// Dynamic operand. None answers: FLT 0 [?].
Primitive Provided(const Graph& g, int dyn, EvalContext& ctx) {
    Primitive v = Primitive::Float(0);
    if (dyn < 0 || !ctx.instance || g.type_of(dyn) != kDynamic) return v;
    auto path = g.DynamicPath(dyn);
    if (path.empty() || !ctx.instance->sm().ProvideValue(path.back(), v)) v = Primitive::Float(0);
    return v;
}

// STUConfigVarExpressionTyped (0xf6c7b0): a stack of StatescriptPrimitives; the result keeps its type.
class TypedExpressionEvaluator : public Evaluator {
public:
    bool DependsOnExternal(const Graph& g, int cv, EvalContext& ctx) const override {
        return ExprDependsOnExternal(g, cv, ctx);
    }
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override {
        ExprData d = ReadExpr(g, cv);
        Primitive st[8];
        int top = -1;
        Primitive cv_cache[4], dyn_cache[4];
        unsigned cv_cached = 0, dyn_cached = 0;
        uint32_t entity = kInvalidEntity;  // op 35 register
        size_t pc = 0;
        auto arg = [&]() -> size_t { return pc + 1 < d.ops.size() ? d.ops[pc + 1] : 0; };
        if (d.ops.empty()) {
            out = Primitive::Float(0);
            return true;
        }
        while (pc < d.ops.size() && d.ops[pc] != 0) {
            uint8_t op = d.ops[pc];
            if (top >= 7 && (op == 30 || op == 31 || op == 70)) break;  // the client has no bounds check
            switch (op) {
            case 1: pc += arg(); continue;
            case 2:
                if (!st[top].Truthy()) pc += arg();
                else top--, pc += 2;
                continue;
            case 3:
                if (st[top].Truthy()) pc += arg();
                else top--, pc += 2;
                continue;
            case 4: {
                bool t = st[top--].Truthy();
                pc += t ? 2 : arg();
                continue;
            }
            case 5: {
                bool t = st[top--].Truthy();
                pc += t ? arg() : 2;
                continue;
            }
            case 30: {
                size_t a = arg();
                Primitive v = Primitive::Float(0);
                if (a < 4 && (cv_cached >> a & 1)) v = cv_cache[a];
                else {
                    if (a >= d.cvs.size() || !Evaluate(ctx, d.cvs[a], v)) v = Primitive::Float(0);
                    if (a < 4) cv_cache[a] = v, cv_cached |= 1u << a;
                }
                st[++top] = v;
                pc += 2;
                continue;
            }
            case 35: {  // "on": the next op 31 reads in this entity's Statescript (invalid id = no entity)
                Primitive e = st[top--];
                entity = e.type == PrimType::Entity ? e.AsU32() : kInvalidEntity;
                pc += 1;
                continue;
            }
            case 31: {
                size_t a = arg();
                Primitive v = Primitive::Float(0);
                if (!IsInvalidEntity(entity)) {  // read in another entity (instance vfunc +0xE8), not cached
                    auto path = a < d.dyn.size() ? g.DynamicPath(d.dyn[a]) : std::vector<uint64_t>{};
                    if (path.empty() || !ctx.instance->sm().ReadEntityVar(entity, path, v)) v = Primitive::Float(0);
                    entity = kInvalidEntity;
                    st[++top] = v;
                    pc += 2;
                    continue;
                }
                entity = kInvalidEntity;
                if (a < 4 && (dyn_cached >> a & 1)) v = dyn_cache[a];
                else {
                    if (a >= d.dyn.size() || !Evaluate(ctx, d.dyn[a], v)) v = Primitive::Float(0);
                    if (a < 4) dyn_cache[a] = v, dyn_cached |= 1u << a;
                }
                st[++top] = v;
                pc += 2;
                continue;
            }
            case 32: case 33: case 34: {  // var[] a: T → value / key / index
                size_t a = arg();
                const Var* dict;
                int i = DictLookup(g, a < d.dyn.size() ? d.dyn[a] : -1, ctx, entity, st[top], &dict);
                entity = kInvalidEntity;
                if (op == 34) st[top] = Primitive::Int(i);
                else if (i < 0) st[top] = Primitive::Int(0);
                else st[top] = op == 32 ? dict->dict()[i].second : dict->dict()[i].first;
                pc += 2;
                continue;
            }
            case 36: {
                size_t a = arg();
                if (top >= 7) break;
                st[++top] = Provided(g, a < d.dyn.size() ? d.dyn[a] : -1, ctx);
                pc += 2;
                continue;
            }
            case 70: st[++top] = Primitive::Float(arg() < d.consts.size() ? d.consts[arg()] : 0.0f); pc += 2; continue;
            default: break;
            }
            // One-byte operators on scalars.
            auto bin = [&](auto f) {
                Primitive r = st[top--];
                st[top] = f(st[top], r);
            };
            auto flt = [](float x) { return Primitive::Float(x); };
            auto byte = [](bool b) { return Primitive::Byte(b); };
            bool vec = top >= 0 && (st[top].type == PrimType::Vec3 || (top >= 1 && st[top - 1].type == PrimType::Vec3));
            switch (op) {
            case 100: case 101: case 102: case 103: case 106: case 165: case 166:
            case 167: case 168: case 169: case 170: case 171: case 172:
                if (vec || op >= 167) {
                    Unmodelled(ctx, op, true);
                    out = Primitive::Float(0);
                    return true;
                }
                break;
            default: break;
            }
            switch (op) {
            case 100: bin([&](auto& l, auto& r) { return flt(l.ToFloat() + r.ToFloat()); }); break;
            case 101: bin([&](auto& l, auto& r) { return flt(l.ToFloat() - r.ToFloat()); }); break;
            case 102: bin([&](auto& l, auto& r) { return flt(l.ToFloat() * r.ToFloat()); }); break;
            case 103: bin([&](auto& l, auto& r) { return flt(r.ToFloat() == 0 ? 0 : l.ToFloat() / r.ToFloat()); }); break;
            case 104:
                bin([&](auto& l, auto& r) {
                    float a = l.ToFloat(), b = r.ToFloat();
                    return flt(b == 0 ? 0 : a - float(int32_t(a / b)) * b);
                });
                break;
            case 105: bin([&](auto& l, auto& r) { return flt(l.ToFloat() <= 0 ? 0 : std::pow(l.ToFloat(), r.ToFloat())); }); break;
            case 106: st[top] = flt(-st[top].ToFloat()); break;
            case 120: st[top] = byte(!st[top].Truthy()); break;
            case 140: bin([&](auto& l, auto& r) { return byte(l.ToFloat() - kFloatEpsilon <= r.ToFloat()); }); break;
            case 141: bin([&](auto& l, auto& r) { return byte(l.ToFloat() < r.ToFloat() - kFloatEpsilon); }); break;
            case 142: bin([&](auto& l, auto& r) { return byte(l.ToFloat() + kFloatEpsilon >= r.ToFloat()); }); break;
            case 143: bin([&](auto& l, auto& r) { return byte(l.ToFloat() > r.ToFloat() + kFloatEpsilon); }); break;
            case 144: bin([&](auto& l, auto& r) { return byte(LooselyEqual(r, l)); }); break;
            case 145: bin([&](auto& l, auto& r) { return byte(!LooselyEqual(r, l)); }); break;
            case 161: st[top] = flt(std::sin(st[top].ToFloat())); break;
            case 162: st[top] = flt(std::cos(st[top].ToFloat())); break;
            case 163: st[top] = flt(std::sin(st[top].ToFloat() * 0.017453292f)); break;
            case 164: st[top] = flt(std::cos(st[top].ToFloat() * 0.017453292f)); break;
            case 165: case 166: st[top] = flt(std::fabs(st[top].ToFloat())); break;
            case 200: bin([&](auto& l, auto& r) { return flt(std::min(l.ToFloat(), r.ToFloat())); }); break;
            case 201: bin([&](auto& l, auto& r) { return flt(std::max(l.ToFloat(), r.ToFloat())); }); break;
            case 203: st[top] = flt(std::floor(st[top].ToFloat())); break;
            case 204: st[top] = flt(std::ceil(st[top].ToFloat())); break;
            case 205: st[top] = Primitive::Int(int32_t(Round(st[top].ToFloat()))); break;
            default:
                Unmodelled(ctx, op, true);
                out = Primitive::Float(0);
                return true;
            }
            pc += 1;
        }
        out = top >= 0 ? st[top] : Primitive::Float(0);
        return true;
    }
};

// STUConfigVarExpression (0xf6adc0): the same bytecode on a float stack; the result is FLT. Op 35 is not
// supported there (it ends the evaluation, §3.4).
class FloatExpressionEvaluator : public Evaluator {
public:
    bool DependsOnExternal(const Graph& g, int cv, EvalContext& ctx) const override {
        return ExprDependsOnExternal(g, cv, ctx);
    }
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override {
        ExprData d = ReadExpr(g, cv);
        float st[8] = {};
        int top = -1;
        size_t pc = 0;
        auto arg = [&]() -> size_t { return pc + 1 < d.ops.size() ? d.ops[pc + 1] : 0; };
        while (pc < d.ops.size() && d.ops[pc] != 0) {
            uint8_t op = d.ops[pc];
            switch (op) {
            case 1: pc += arg(); continue;
            case 2:
                if (st[top] == 0.0f) pc += arg();
                else top--, pc += 2;
                continue;
            case 3:
                if (st[top] == 0.0f) top--, pc += 2;
                else pc += arg();
                continue;
            case 4:
                pc += st[top] != 0.0f ? 2 : arg();
                top--;
                continue;
            case 5:
                pc += st[top] == 0.0f ? 2 : arg();
                top--;
                continue;
            case 30: {
                size_t a = arg();
                st[++top] = a < d.cvs.size() ? EvaluateFloat(ctx, d.cvs[a], 0.0f) : 0.0f;
                pc += 2;
                continue;
            }
            case 31: {
                size_t a = arg();
                st[++top] = a < d.dyn.size() ? EvaluateFloat(ctx, d.dyn[a], 0.0f) : 0.0f;
                pc += 2;
                continue;
            }
            case 32: case 33: case 34: {  // as the typed ops, on floats (key = position when numeric)
                size_t a = arg();
                const Var* dict;
                int i = DictLookup(g, a < d.dyn.size() ? d.dyn[a] : -1, ctx, kInvalidEntity, Primitive::Float(st[top]), &dict);
                if (op == 34) st[top] = float(i);
                else st[top] = i < 0 ? 0.0f : NumFloat(op == 32 ? dict->dict()[i].second : dict->dict()[i].first);
                pc += 2;
                continue;
            }
            case 36: {
                size_t a = arg();
                st[++top] = NumFloat(Provided(g, a < d.dyn.size() ? d.dyn[a] : -1, ctx));
                pc += 2;
                continue;
            }
            case 70: st[++top] = arg() < d.consts.size() ? d.consts[arg()] : 0.0f; pc += 2; continue;
            default: break;
            }
            auto bin = [&](auto f) {
                float r = st[top--];
                st[top] = f(st[top], r);
            };
            switch (op) {
            case 100: bin([](float l, float r) { return l + r; }); break;
            case 101: bin([](float l, float r) { return l - r; }); break;
            case 102: bin([](float l, float r) { return l * r; }); break;
            case 103: bin([](float l, float r) { return r == 0 ? 0.0f : l / r; }); break;
            case 104: bin([](float l, float r) { return r == 0 ? 0.0f : l - float(int32_t(l / r)) * r; }); break;
            case 106: st[top] = -st[top]; break;
            case 120: st[top] = st[top] == 0.0f ? 1.0f : 0.0f; break;
            case 140: bin([](float l, float r) { return l - kFloatEpsilon <= r ? 1.0f : 0.0f; }); break;
            case 141: bin([](float l, float r) { return l < r - kFloatEpsilon ? 1.0f : 0.0f; }); break;
            case 142: bin([](float l, float r) { return l + kFloatEpsilon >= r ? 1.0f : 0.0f; }); break;
            case 143: bin([](float l, float r) { return l > r + kFloatEpsilon ? 1.0f : 0.0f; }); break;
            case 144: bin([](float l, float r) { return (l < r - kFloatEpsilon || r + kFloatEpsilon < l) ? 0.0f : 1.0f; }); break;
            case 145: bin([](float l, float r) { return (l < r - kFloatEpsilon || r + kFloatEpsilon < l) ? 1.0f : 0.0f; }); break;
            case 161: st[top] = std::sin(st[top]); break;
            case 162: st[top] = std::cos(st[top]); break;
            case 163: st[top] = std::sin(st[top] * 0.017453292f); break;
            case 164: st[top] = std::cos(st[top] * 0.017453292f); break;
            case 165: case 166: st[top] = std::fabs(st[top]); break;
            case 200: bin([](float l, float r) { return std::min(l, r); }); break;
            case 201: bin([](float l, float r) { return std::max(l, r); }); break;
            case 203: st[top] = std::floor(st[top]); break;
            case 204: st[top] = std::ceil(st[top]); break;
            case 205: st[top] = Round(st[top]); break;
            default:
                Unmodelled(ctx, op, false);
                out = Primitive::Float(0);
                return true;
            }
            pc += 1;
        }
        out = Primitive::Float(top >= 0 ? st[top] : 0.0f);
        return true;
    }
};

}  // namespace

void EvaluatorRegistry::RegisterConstant(uint32_t type_hash, const Primitive& value) {
    Register(type_hash, std::make_shared<FunctionEvaluator>([value](const Graph&, int, EvalContext&, Primitive& out) {
                 out = value;
                 return true;
             }));
}

EvaluatorRegistry EvaluatorRegistry::Builtin() {
    EvaluatorRegistry r;
    for (auto& l : kLiteralConfigVars) r.Register(l.type, std::make_shared<LiteralEvaluator>(l.kind));
    r.Register(kGetOwner, std::make_shared<OwnerEvaluator>());
    r.Register(kDynamic, std::make_shared<DynamicEvaluator>());
    RegisterHeroEvaluators(r);
    RegisterMatchEvaluators(r);  // match / objective families (T055)
    r.Register(kExpression, std::make_shared<FloatExpressionEvaluator>());
    r.Register(kExpressionTyped, std::make_shared<TypedExpressionEvaluator>());
    return r;
}

bool Evaluate(EvalContext& ctx, int cv, Primitive& out) {
    if (cv < 0 || !ctx.instance) return false;
    const Graph& g = ctx.instance->graph();
    uint32_t type = g.type_of(cv);
    const Evaluator* e = ctx.instance->sm().evaluators().Find(type);
    if (!e) {
        ctx.instance->sm().unmodelled_config_vars[type]++;
        return false;
    }
    return e->Get(g, cv, ctx, out);
}

bool EvaluateBool(EvalContext& ctx, int cv, bool dflt) {
    Primitive p;
    return Evaluate(ctx, cv, p) ? p.Truthy() : dflt;
}

int32_t EvaluateInt(EvalContext& ctx, int cv, int32_t dflt) {
    Primitive p;
    if (!Evaluate(ctx, cv, p)) return dflt;
    switch (p.type) {
    case PrimType::Byte: return (p.bits & 0xFF) ? 1 : 0;
    case PrimType::Int: return p.AsInt();
    case PrimType::Float: return int32_t(Round(p.AsFloat()));
    default: return dflt;
    }
}

float EvaluateFloat(EvalContext& ctx, int cv, float dflt) {
    Primitive p;
    if (!Evaluate(ctx, cv, p)) return dflt;
    switch (p.type) {
    case PrimType::Byte:
    case PrimType::Int:
    case PrimType::Float: return p.ToFloat();
    default: return dflt;
    }
}

uint64_t EvaluateGuid(EvalContext& ctx, int cv, uint64_t dflt) {
    Primitive p;
    return Evaluate(ctx, cv, p) && p.type == PrimType::Int64 ? p.bits : dflt;
}

uint32_t EvaluateEntity(EvalContext& ctx, int cv) {
    Primitive p;
    return Evaluate(ctx, cv, p) && p.type == PrimType::Entity ? p.AsU32() : kInvalidEntity;
}

namespace {
constexpr uint32_t kArray = 0x13ba10f6, kIterable = 0x8cb6be1c;

void IterateInto(EvalContext& ctx, int cv, int depth, std::vector<DictEntry>& out) {
    const Graph& g = ctx.instance->graph();
    if (depth < 4 && g.IsA(cv, kArray)) {  // elements in order, nested arrays flattened
        for (int e : g.Objects(g.value(cv), kConfigVars))
            if (e >= 0) IterateInto(ctx, e, depth + 1, out);
        return;
    }
    if (g.IsA(cv, kIterable)) {  // the evaluator's own iterator (+0xE0 / +0xE8): host game state
        const Evaluator* e = ctx.instance->sm().evaluators().Find(g.type_of(cv));
        if (!e || !e->IterateValues(g, cv, ctx, out)) ctx.instance->sm().unmodelled_config_vars[g.type_of(cv)]++;
        return;
    }
    if (g.type_of(cv) == kDynamic)
        if (const Var* v = ctx.instance->FindDictionary(g.DynamicPath(cv))) {
            out.insert(out.end(), v->dict().begin(), v->dict().end());
            return;
        }
    Primitive v = Primitive::Byte(false);
    if (!Evaluate(ctx, cv, v)) v = Primitive::Byte(false);
    out.emplace_back(Primitive::Byte(false), v);
}
}  // namespace

std::vector<DictEntry> Iterate(EvalContext& ctx, int cv) {
    std::vector<DictEntry> out;
    if (cv >= 0 && ctx.instance) IterateInto(ctx, cv, 0, out);
    return out;
}

bool DependsOnExternal(EvalContext& ctx, int cv) {
    if (cv < 0 || !ctx.instance) return false;
    const Graph& g = ctx.instance->graph();
    const Evaluator* e = ctx.instance->sm().evaluators().Find(g.type_of(cv));
    return e && e->DependsOnExternal(g, cv, ctx);
}

}  // namespace ss
