# Statescript config vars and expressions (client build 24919)

How the client computes the values that Statescript nodes consume: conditions, switch cases, SetVar values,
targets, UI values. Every such input is a **config var** (`STUConfigVar` subclass) evaluated by a per-type
singleton; the most common one is an **expression** (a small stack-machine bytecode). Written for the
clean-room runtime (T016, `systems/statescript`). T038.

Context: [StatescriptRuntime.md](StatescriptRuntime.md) §1.7 (values `StatescriptPrimitive`, var bags, paths),
§1.8 (evaluator registry and binding), §3.8 (variables and dependencies);
[StatescriptNodeCatalogue.md](StatescriptNodeCatalogue.md) (all 328 config-var types).

**Addresses:** RVAs (`runtime = 0x140000000 + RVA`, `Ghidra = 0x7ff64ffe0000 + RVA`). **Tags:** [G] read in
Ghidra, [L] checked against the graph data (all 370 graphs of the build, dumped offline with
`content_dump` + `stu_dump`; the 296 resident ones are the same assets), [?] inferred.

---

## 1. The evaluator interface

### 1.1 Binding [G]

A config var in a graph is an STU object; `+0x08` (`0x83e83924`, 0 in the file [L]) is a runtime slot.
`LinkGraph` (`0xf517f0`) fills it once per graph with the **evaluator singleton** of the object's exact STU
type (registry lookup `0xf433a0`, keyed by the type's inheritance object), else the `STUConfigVar` base
evaluator (`0xf435c0`) (StatescriptRuntime.md §1.8). Evaluators are stateless 8-byte objects (a vtable).
The config var is passed back to its evaluator on every call, so all per-use data comes from the STU fields.

### 1.2 Vtable [G]

Evaluator vtables are 0xE0 bytes (28 slots) and lie back to back in `.rdata` (e.g. `STUConfigVarExpression`
`0x160ae38` + 0xE0 = `STUConfigVarExpressionTyped` `0x160af18`). Slots `+0x00…+0xC8` are the generic STU
object slots (identical in every config-var evaluator except `+0x08/+0x10/+0x18/+0x28`, which are the per-type
STU info / inheritance accessors). The two Statescript slots:

| slot | signature | meaning |
|---|---|---|
| `+0xD0` | `bool GetValue(Evaluator*, StatescriptInstance* inst, STUConfigVar* cv, StatescriptPrimitive* out)` | compute the value of `cv` in the context of `inst`; writes `*out` (any `StatescriptPrimitive` type) and returns true on success. `out` is pre-initialised by callers to INT 0 |
| `+0xD8` | `bool DependsOnExternal(Evaluator*, StatescriptInstance* inst, STUConfigVar* cv)` [?] | base (`0x6119c0`): false. Dynamic var (`0xf5c160`): true if the var currently belongs to a state (user priority ≠ 0, instance ≠ 0) or its path has more than one non-special element (it crosses into another bag/entity). Expressions (`0xf6beb0`): true if the bytecode contains op 35 (`on entity`), or any operand config var / dynamic var answers true. Wrapper evaluators forward to their operand (e.g. `0xd98b40`). Name inferred: "value can change without a variable-change notification on this instance" |

Some evaluator families add slots after `+0xD8` (the iterable config vars, §1.5).

**Context argument.** `inst` is the `StatescriptInstance*` (not the state): the dynamic-var evaluator
(`0xf5c120`) reads through `inst+0x98` (the instance var bag) vfunc `+0xF8`, and the expression evaluators
call instance vfuncs (§1.4). States reach it through `state+0x78`.

### 1.3 Reading a config var from node code: the typed getters [G]

Node implementations do not use the raw value directly; they call one of these helpers (all: null `cv` →
default; evaluator failure → default; strings are freed):

| RVA | form | returns |
|---|---|---|
| `0xcffa40` | `GetBool(state, cv, default)` | truthiness of the value (`0xee8a80`, table in RuntimeDoc §1.7) |
| `0xcffac0` / `0xcffe50` | `GetInt(state|inst, cv, default)` | BYTE → 0/1, INT → value, FLT → rounded half away from zero; other types → default |
| `0xcffbc0` / `0xcfff50` | `GetFloat(state|inst, cv, default)` | BYTE → 0/1, INT → (float), FLT; other types → default |
| `0xcffca0` / `0xd00030` | `GetGUID(state|inst, cv, default)` | INT64 only, else default |
| `0xcffd30` | `GetEntity(state, &out, cv)` | ENTITY id, else the invalid entity id (global `0x183a0c8`) |

(`state` versions take the state object and use `state+0x78` as the instance.) 294 call sites use
`GetFloat(inst)`, 212 `GetFloat(state)`: most node inputs are read as floats.

### 1.4 Instance services used by evaluators [G]

The evaluators reach game state only through a few instance vfuncs (client instance vtable `0x15da3c8`;
engine base `0x1604df0` has stubs where marked):

| vfunc | RVA (client) | used by | does |
|---|---|---|---|
| `+0xB8` | `0x107bb90` (base `0xf4bb40`) | expression arithmetic on ENTITY | entity id → world position (VEC3): SyncMgr (`inst+0x10`) vfunc `+0x248` looks up the entity, then its transform; false (0,0,0) if not found |
| `+0xC8` | `0xf4d580` | op 36 | ask the sync manager's **value providers** (list `SyncMgr+0x410`, count `+0x418`, walked last to first) to supply the value of a dynamic var; first provider that returns true wins; false if none. Only for `STUConfigVarDynamic` operands. Which objects register as providers: not traced [?] |
| `+0xD0` | `0xf4d630` | float expression ops 37–39 | index/search in an iterable config var (float index out) |
| `+0xD8` | `0xf4d8e0` | typed expression ops 37–39 | index/search in an iterable config var: key BYTE/INT/FLT = position (FLT rounded half away from zero), else search for an entry equal (`0xee99c0`) to the key; out = `{key, value}` pair + int index; not found: key = value = INT 0, index −1 |
| `+0xE0` | `0xf4dc00` | op 40 | number of entries of an iterable config var |
| `+0xE8` | `0x107ef80` (base: no-op) | op 31 after op 35 | read a dynamic var in **another entity's** Statescript: SyncMgr vfunc `+0xD0(entity, path, out)` |
| `+0xF0` | `0x107efa0` (base: 0) | ops 32–34 | fetch the entries (`{Primitive key, Primitive value}`, 0x20 B each) and count of a Dictionary var, optionally on another entity |

**Iterable config vars** (ops 37–40, iterator `0xf5a740`/`0xf5a7f0`): `STUConfigVarArray` (`13ba10f6`,
`+0x10 m_configVars`: its elements, nested arrays flattened up to depth 4), subclasses of `STUConfigVarIterable`
(`8cb6be1c`: its `GetValue` `0xf5a6e0` tail-calls the evaluator's own slot `+0xE0`, and the iterator calls slot `+0xE8`; these evaluator vtables are longer than 0xE0 [G]), or anything else that resolves to a
Dictionary var (`0xf4d330`). No resident or offline graph uses ops 37–40 [L], so only `STUConfigVarArray`
matters in practice for other node types.

### 1.5 Caching and dependencies

- **No caching between evaluations** [G]. Every `GetValue` recomputes. Inside one expression evaluation the
  first four `m_configVars` and the first four `m_dynamicVars` are memoised (bit masks at the start of the
  interpreter), so an operand used twice is read once.
- **Dependencies are declared by the nodes, not discovered from the expressions**: states subscribe on begin
  to the var paths in their STU lists `+0xA8`/`+0xB8` (`STUConfigVarDynamic*[]`), and a change re-runs their
  exit evaluation (RuntimeDoc §3.8). Conditions and actions evaluate on execution. External state (health,
  team, positions) reaches states through the component-watcher bits (RuntimeDoc §3.8). The `+0xD8` query
  above is the only per-config-var dependency information the client has.

---

## 2. Values

The value type is `StatescriptPrimitive` (16 B, RuntimeDoc §1.7): BYTE 1, INT 2, FLT 3, INT64 4, STRING 5,
VEC3 6, INTVEC4 7, SS_INSTANCEID 8, FLT16VEC4 9, ColorRGBA 10, ENTITY 11. Helpers [G]: set BYTE `0xee8b30`,
set INT `0xee8ba0`, set FLT `0xee8c00`, set VEC3 `0xee8d40`, set FLT16VEC4 `0xee8f40`, to float `0xee90e0`
(BYTE 0/1, INT, FLT, else 0), copy `0xeea470`, truthy `0xee8a80`/`0xee97b0`, equal `0xee99c0`.

**Equality** (`0xee99c0`, used by `==`/`!=` and dictionary search) [G]:

| a \ b | rule |
|---|---|
| b is BYTE | `truthy(a) == (b != 0)` |
| a is BYTE | `(a != 0) == truthy(b)` |
| INT, FLT | INT–INT exact; any mix with FLT: `|a − b| ≤ ε`; other b types: false |
| INT64 | b INT64 and equal |
| STRING | both non-null and equal strings (`0xeea860`); two null strings are **not** equal |
| INTVEC4, ENTITY, SS_INSTANCEID | b has the same type and equal bits |
| VEC3, FLT16VEC4, ColorRGBA, other | the 16 bytes (payload + type) compare equal: same type, bit-exact |

`ε = 1.1920929e-05` (`0x162c6e8`, 100 × FLT_EPSILON).

---

## 3. Expressions

### 3.1 Two STU types, two interpreters [G][L]

| type | hash | evaluator | stack | uses (370 graphs) |
|---|---|---|---|---|
| `STUConfigVarExpression` | `cc3a08f4` | `0xf6adc0` (vtable `0x160ae38`) | `float[8]`: every operand is converted to float; result FLT | 280 |
| `STUConfigVarExpressionTyped` (name by CRC, T019) | `ec502a81` | `0xf6c7b0` (vtable `0x160af18`) | `StatescriptPrimitive[8]`: operands keep their type; result = the top value with its type | 3,740 |

Same STU layout (0x68 B; the typed one adds no fields) and the same opcodes. "Typed" is literally the
difference: the plain expression is a float calculator (vector ops yield 0, entity operands yield 0, `==` is
an ε-compare of floats), the typed one handles entities, vectors and typed equality.

### 3.2 Layout (`STUConfigVarExpression`, 0x68) [G][L]

| off | field | type | use |
|---|---|---|---|
| +0x08 | `0x83e83924` | evaluator slot | runtime |
| +0x10 | `m_expression.m_fragments` | `STUConfigVarExpressionFragment[]` (`m_text`, `m_guid`) | editor tokens of the source text; texts are empty in shipped data, guids name the referenced vars (0x0D8…) or 0x0A4… [L]. **Not read by the evaluator** [G] |
| +0x20 | `m_expression.m_opcodes` | `u8[]` | bytecode, 0-terminated (the interpreter stops at a 0 byte) |
| +0x30 | `m_expression.m_dynamicVars` | `STUConfigVarDynamic*[]` | variable operands (ops 31–34, 36) |
| +0x40 | `m_expression.0x1b183605` | `f32[]` | float constants (op 70) |
| +0x58 | `m_configVars` | `STUConfigVar*[]` | config-var operands (ops 30, 37–40): any config var, including nested expressions |

Maxima over all graphs [L]: 118 opcode bytes, 6 config vars, 8 dynamic vars, 6 constants, stack depth 6. An empty opcode
list yields FLT 0.

### 3.3 Machine [G]

- Stack of 8 entries, index starts at −1; no bounds checks (the data never exceeds it).
- `pc` walks the byte list; each opcode is 1 byte, 16 of them take a 1-byte **unsigned** argument.
- **Jumps are relative to the jump opcode's own position** (`pc += arg`) and always forward [L].
- A 0 byte ends evaluation: `out = copy(stack[top])`. **Unknown opcode**: `out = FLT 0`, stop.
- The evaluator always returns true.
- `entity_ctx` register (op 35): initially "none" (the invalid entity id); consumed and reset by the next
  op 31–34.

### 3.4 Opcode table

`a` = argument byte, `T` = top of stack, `S` = the entry below it. Binary ops pop `T`, combine with `S` and
replace `S` (`l` = S = left operand, `r` = T = right operand). Uses = typed / float, over all 370 graphs.

| op | name | stack effect | typed semantics [G] | uses |
|---|---|---|---|---|
| 0 | end | | result = `T` | 3738/280 |
| 1 | jmp a | | `pc += a` | 423/18 |
| 2 | and a | | if `T` falsy: jump, **keep** `T`; else pop, continue (`l && r`, value of the deciding operand, not a bool) | 426/34 |
| 3 | or a | | if `T` truthy: jump, keep `T`; else pop (`l \|\| r`) | 279/13 |
| 4 | jf a | pop | pop; if falsy jump (`c ? then : else`; then-branch ends with `jmp` over the else-branch) | 433/19 |
| 5 | jt a | pop | pop; if truthy jump | 16/19 |
| 30 | cv a | push | `m_configVars[a]` → its evaluator's `GetValue`; failure / null → FLT 0 | 2534/28 |
| 31 | var a | push | `m_dynamicVars[a]` read through the instance bag (path walk RuntimeDoc §1.7; missing var → FLT 0); after op 35: read in that entity's Statescript (inst `+0xE8`) | 3591/556 |
| 32 | var[] a | T → value | key `T`, Dictionary var `m_dynamicVars[a]` (inst `+0xF0`, on `entity_ctx` if set): key BYTE/INT/FLT = position (FLT rounded), else equality search; → value, INT 0 if not found | 291/8 |
| 33 | var[].key a | T → key | as 32, → key | 0 |
| 34 | var[].idx a | T → index | as 32, → INT index (−1 not found) | 0 |
| 35 | on | pop | `entity_ctx = T` if ENTITY, else none | 157/0 (float: not supported, ends evaluation) |
| 36 | provider a | push | value of `m_dynamicVars[a]` from the sync manager's value providers (inst `+0xC8`) | 98/3 |
| 37/38/39 | cv[] a | T → value / key / INT index | index or search in iterable config var `m_configVars[a]` (inst `+0xD8`) | 0 |
| 40 | count a | push INT | entries of iterable `m_configVars[a]` (inst `+0xE0`) | 0 |
| 70 | const a | push FLT | constants`[a]` | 2933/184 |
| 100 | + | 2→1 | both vector-like (VEC3 or ENTITY → position): component-wise → VEC3; else float(l) + float(r) → FLT | 447/50 |
| 101 | − | 2→1 | as + | 293/50 |
| 102 | * | 2→1 | vec·vec component-wise; vec·scalar / scalar·vec scales → VEC3; scalars → FLT | 332/51 |
| 103 | / | 2→1 | scalar: `r == 0 → 0`; vec/scalar: scalar 0 → zero vector [?]; vec/vec component-wise, zero divisor components → 0 [?] → VEC3 / FLT | 212/30 |
| 104 | % | 2→1 | `l − trunc(l / r)·r` (C `fmod`), `r == 0 → 0` → FLT (float interpreter; typed path identical by structure [?]) | 10/0 |
| 105 | pow | 2→1 | `l ≤ 0 → 0`, else `powf(l, r)` → FLT | 6/0 |
| 106 | neg | 1→1 | vector: negate components → VEC3; scalar → FLT `−x` | 46/0 |
| 120 | ! | 1→1 | BYTE `!truthy(T)` | 377/29 |
| 140 | <= | 2→1 | BYTE `l ≤ r + ε` (`l − ε ≤ r`) | 81/6 |
| 141 | < | 2→1 | BYTE `l < r − ε` | 114/30 |
| 142 | >= | 2→1 | BYTE `l + ε ≥ r` | 134/4 |
| 143 | > | 2→1 | BYTE `l > r + ε` | 332/43 |
| 144 | == | 2→1 | BYTE `equal(T, S)` (§2); float interpreter: `|l − r| ≤ ε` | 769/93 |
| 145 | != | 2→1 | BYTE `!equal` | 175/2 |
| 160 | sqrt | 1→1 | `x ≤ 1.18e-36 → 0`, else `sqrt(x)` (`0x8597a0`) → FLT. **Prometheus calls this inverse sqrt: wrong** | 0 |
| 161 / 162 | sin / cos | 1→1 | radians → FLT | 0/4 |
| 163 / 164 | sind / cosd | 1→1 | degrees (`× 0.017453292`) → FLT | 3/6, 3/6 |
| 165, 166 | abs / len | 1→1 | vector → length (`0x8766b0`) FLT; scalar → `|x|` FLT (both opcodes identical) | 3+22/3 |
| 167 | dist | 2→1 | vectors: `|l − r|` FLT; otherwise 0 | 41/0 |
| 168 | dot | 2→1 | vectors: dot FLT; otherwise 0 | 45/0 |
| 169 | cross | 2→1 | vectors: cross VEC3; otherwise 0 | 12/0 |
| 170 | norm | 1→1 | vector: normalised VEC3 (length² < 1.18e-36 → a default vector, global `0x188f710`, runtime-initialised [?]); scalar: sign, `x < 0 → −1`, else `1` | 68/0 |
| 171 | vec3 | 3→1 | `vec3(float(a), float(b), float(c))` in push order | 300/0 |
| 172 | half4 | 4→1 | FLT16VEC4 from 4 floats (`0xee8360` float → half) in push order | 0 |
| 173/174/175 | x / y / z | 1→1 | VEC3 component → FLT; other types → FLT 0 [?] (entity operands are not converted here) | 30/18/10 |
| 200 / 201 | min / max | 2→1 | on floats → FLT | 18/16, 31/9 |
| 202 | clamp | 3→1 | `clamp(v, lo, hi)` with `v` pushed first; the two bounds are sorted first → FLT | 9/0 |
| 203 / 204 | floor / ceil | 1→1 | → FLT | 31/1, 68/0 |
| 205 | round | 1→1 | half away from zero → **INT** (float interpreter: FLT) | 3/0 |
| 206 | random | 2→1 | `0xf6c310(l, r)`: random between the two values, typed [?] | 9/4 |
| 207 | randint | 2→1 | `round(l) + rand_int(round(|r − l|))` (sign follows `r − l`) → FLT | 5/0 |

Float interpreter differences [G]: every load converts to float (BYTE 0/1, INT, FLT; other types 0), op 35
is unknown (ends evaluation), vector ops 167–169 and 171/172 pop their operands and push 0, 173–175 give 0,
170 = sign, 165/166 = `|x|`, comparisons and `==` use ε on floats, results are FLT, `!` gives 1.0/0.0.

### 3.5 Grammar (decoded form)

The bytecode is the postfix form of this expression language (the editor's source text is not shipped;
`m_fragments` only keeps the token boundaries):

```
expr    := cond
cond    := or ( '?' expr ':' expr )?          # jf/jt + jmp
or      := and ( '||' and )*                  # op 3
and     := cmp ( '&&' cmp )*                  # op 2
cmp     := sum ( ('=='|'!='|'<'|'<='|'>'|'>=') sum )?
sum     := prod ( ('+'|'-') prod )*
prod    := unary ( ('*'|'/'|'%') unary )*
unary   := ('-'|'!') unary | call | atom
call    := name '(' expr (',' expr)* ')'     # sqrt sin cos sind cosd abs len dist dot cross norm
                                              # vec3 half4 x y z min max clamp floor ceil round random randint pow count
atom    := number | $var | entity '.' $var | $var '[' expr ']' | cvN | '(' expr ')'
```

Decoder: `systems/data/statescript/tools/expr.py` (`expr.py graph.json` prints every expression of a
`stu_dump`ed graph; `decode()` is importable). All 4,020 expressions of the 370 graphs decode without an
unknown opcode or stack underflow [L]. Notation: `$813` = var `0x0D8…813` (instance bag path),
`$ent.X` = entity bag, `E.$X` = `$X` in entity `E`'s Statescript (op 35), `provider($X)` = op 36.

---

## 4. Catalogue of evaluators

**Full per-type table: [StatescriptConfigVarCatalogue.md](StatescriptConfigVarCatalogue.md)** (the 278 types
used by any graph of the build, incl. the 243 used by the resident graphs; inputs → output, what `GetValue`
computes, failure behaviour, what it reads, client class, uses, tag). Machine-readable for T016:
`systems/data/statescript/configvar_catalogue.json` (all 328 types; `getvalue` / `depends_on_external` RVAs,
evaluator vtable, fields, uses) built by `tools/configvar_catalogue.py` from `configvar_semantics.json` (the
per-type readings, the source of truth for the semantics columns).

How it was made: for every used type the evaluator vtable's `+0xD0` gives its `GetValue`; the 219 distinct
functions were decompiled (Ghidra) and read, callees one or two levels deep where needed [G]; 29 resource types
share `0xf69420`, 10 enum/int types share `0xd99240`, 4 types share the constant-false `0xd92900`. The 19 used
types without a client class were described from name, fields and consumers [?].

### 4.1 Families (used types)

| family | types (resident) | resident uses | outputs | reads | notes |
|---|---|---|---|---|---|
| var access | 4 (4) | 11,838 | varies, BYTE | instance vars | `STUConfigVarDynamic` (11,708 uses) = the var path read (RuntimeDoc §1.7); array-contains, priority-holder checks |
| literal | 24 (20) | 10,159 | INT, BYTE, FLT, VEC3, colour, STRING | literal | Int, Bool, Float, String, Vec3, Color, enums (Team, LogicalButton, HealthType, AbilityStatus, …), Int/Float/BoolRandom |
| resource/asset | 40 (38) | 5,666 | INT64 | literal, assets | asset references → their GUID (GameMessage, Identifier, Effect, HardPoint, AnimAlias, UXDisplayText, …); a few read the asset (hero name/icon/role, map name) |
| entity | 63 (55) | 4,792 | FLT, VEC3, BYTE, ENTITY | entity state | GetOwner (3,042), Filter / PassesFilter, positions/vectors (component 0x01 scene, 0x12 movement, 0x15 mover), health (0x28), possession (0x20), controller (0x1F), pet master (0x3D), AI (0x3A) |
| math/logic | 14 (11) | 3,562 | varies, VEC3, FLT | pure | the two expressions (§3), Array, vector math (angles ↔ direction, projection, rotation), FormatString-like pure string builders |
| hero/player | 43 (40) | 858 | BYTE, INT64, ENTITY, INT | local player, entity | local player / possessed view (GameEntityAdmin `+0x220`), hero of an entity (component 0x3F `+0x98`), loadout, profile |
| UI | 25 (25) | 336 | STRING, BYTE | client | rich text, time strings, lobby UI vars, reticle |
| team | 6 (6) | 232 | INT, BYTE | entity | GetTeam (component 0x10 filter bits), hostility tests |
| match/gamemode | 19 (19) | 187 | BYTE, FLT, INT | match state | GetPvPEntity (first component 0x24 in the game EA), round winner / overtime (lobby-EA match object), map, custom game, matchmaking, replay mode |
| input | 4 (4) | 139 | | local player | key-binding names, gamepad, throttle |
| other | 17 (17) | 58 | | client/platform | feature flags, render world, net stats, FPS/ping strings |
| server-only | 19 (4) | 49 | | none on the client | see 4.3 |

### 4.2 What matters for the server

- **Pure** (expressions, literals, asset GUIDs, vector math, Array, random): implement exactly; about half of all
  config-var uses (19.4k of 37.9k resident). Random ones use the client's own RNG (`IntRandom` = `lo + rand % (hi − lo + 1)`, inclusive),
  so client and server values differ by design.
- **Instance vars**: `STUConfigVarDynamic` through the var bag (RuntimeDoc §1.7).
- **Entity state**: the evaluators read ECS components of the entity given by an operand. For the server the
  important inputs are: owner entity (`GetOwner`: SyncMgr `+0x450` → component → entity `+0x94`), filters (team
  / FilterBits of component 0x10, `SyncMgr` vfunc `+0x168` = `0x1073d80`), team (0x10), position and
  orientation (0x01 / 0x12 / 0x15), health pools (0x28), possession (0x20), controller (0x1F), pet master
  (0x3D), hero (0x3F `+0x98`), capture-area occupancy (`e4a99cc5`: area entity component 0x1A `+0x98` list),
  point-in-area (`IsPointInArea`: component 0x06 shape). A server implementation evaluates them against its own
  ECS (`systems/ecs`).
- **Match state**: `GetPvPEntity` = entity of the first component 0x24 (PvPGame) in the game EA
  (`EA+0xB0+0x24*0x18`, `0xc586f0`); none → the invalid id **with success**. `GetRoundWinningTeam`,
  `GetOvertimeRemaining`, `GetMaxOvertimeLength`, `179167ef` (slot `+0x40`) and `63c25b10` (slot `+0x08`) read one
  object from the **lobby** entity admin (scope EA RTTI `0x17caf70` = lobby EA per three independent readings,
  vfunc `+0x90`); i.e. on the client this state arrives through the lobby, not through game entities [G path,
  lobby identity ?].
- **Local player / client / UI** types (≈ 100 types, few uses) only make sense on a client: they read the local
  player (GameEntityAdmin `+0x220`), the camera, the lobby profile (component 0x53), feature flags, net stats.
  A server either never evaluates them (client-only nodes, `m_clientOnly`) or must pick a player.
- **Surprises to reproduce** (details in the table): several evaluators return true without writing `out` (the
  caller's pre-initialised INT 0 then reads as 0/false), some return false after writing; some write only x,y
  of a VEC3; 4 health getters return the entity id's bits as a float when component 0x28 is missing; GetHeroIcon
  (a StringBase) returns an INT64; `StringTime` formatting quirks.

### 4.3 Server-only config vars (no client class) [?]

19 used types have no client evaluator (their `GetValue` is the base `0x6119c0`: false → defaults). 4 are in
resident graphs: `8f3d1c9d` (an iterable **target set**: `m_targets` of VoiceStimulus ×136, Criteria,
SendGameMessage, PlayScript), `8761967f` `UXAuthorityGrey` (iterable target set over `m_observedEntity`,
`m_targets` of SendGameMessage ×29), `4b5d2f3f` (FLT from a team operand, `f26a2fc7`: a per-team number such
as score/progress) and `31993d91` (FLT from an entity). The rest are escort/payload (`GetEscortPos`,
`HasEscortPos`, the escort entity `e4fc31f9`), AI (`GetAIDestination`, AI inputs) and an amount lookup used
by `ModifyHealth` (`6aba9763`, ×8). Their inputs are in the table; their semantics must be designed.

---

## 5. Worked examples

Decoded with `tools/expr.py`; values from the T015 fixtures (`systems/statescript/fixtures/`).

### 5.1 Door `0x042` (`trace_door_042_start` / `_entry0`) [L]

Instance vars at start (fixture): `$813` BYTE 0, `$186` BYTE 1 (after the overrides), `$2bb` INT 0, `$838` INT
0, `$7f3` INT 2, `$9cb`/`$9cc` INT 0.

| node | field | bytecode | decoded | value | observed |
|---|---|---|---|---|---|
| #5 SetVar `$192` | m_value | `31 0, 4 6, 70 0, 1 4, 70 1, 0`; k = [1, 0] | `($813 ? 1 : 0)` | `$813` falsy → jf to `70 1` → **FLT 0** (constants are FLT) | var `192 = FLT 0` ✓ |
| #6 BooleanSwitch | m_condition | `31 0, 70 0, 144, 0` | `($192 == 1)` | FLT 0 vs FLT 1 → BYTE 0 | false subgraph → #26 ✓ |
| #26 Wait | m_timeout | `31 0, 4 6, 31 1, 1 4, 70 0, 0` | `($2bb ? $9cc : 0)` | FLT 0 | finishes in the same ms ✓ |
| #3 Anim | `0x4d475080` | `31 0, 4 6, 70 0, 1 4, 31 1, 0` | `($838 ? 0.01 : $7f3)` | **INT 2** (the else branch leaves `$7f3` with its own type) | (not traced) |
| #23 Bool | m_condition | (plain Dynamic `$838`) | | INT 0 → false | link returns 0 ✓ |
| #1 Bool (entry 0) | m_condition | `31 0, 70 0, 144, 0` | `($186 == 1)` | equal(FLT 1, BYTE 1): BYTE rule → truthy == true → BYTE 1 | true → #7 ✓ |
| #7 SetVar `$192` | m_value | as #5, k = [0, 1] | `($813 ? 0 : 1)` | FLT 1 | var `192 = FLT 1` ✓, switch → #25 ✓ |
| #19 Bool | m_condition | `31 0, 30 0, 144, 2 5, 31 1, 120, 0` | `(($7d3 == Identifier(0x0D8…18D)) && !$813)` | GUID compare (INT64 needs INT64), then `!` | (not traced) |

### 5.2 Music `0x1e5` (lobby entity `0x80000002`, `trace_music_1e5`)

| node | decoded | bytecode | expected |
|---|---|---|---|
| #43 BooleanSwitch | `((GetOvertimeRemaining() > 0) && !0x0e00b16e())` | `30 1, 70 0, 143, 2 5, 30 0, 120, 0` | lobby match object overtime (FLT) `> 0`; with no overtime: BYTE 0, and `&&` returns that BYTE 0 without evaluating the right side. `0x0e00b16e` = session-mode byte == 8 |
| #12 / #13 Bool | `(GetRoundWinningTeam() == Team(0))` / `Team(1)` | `30 1, 30 0, 144, 0` | INT vs INT exact |
| #78 Bool | `((0x63c25b10() == Identifier(0x0D8…18D)) && (0x179167ef() == 2))` | `30 0, 30 1, 144, 2 7, 30 2, 70 0, 144, 0` | reached once in the fixture (CF 1484, during map load); returned **false** ✓ [L] (the lobby match object's GUID slot ≠ `0x0D8…18D` or not available) |
| `$7ee` buckets (#50…) | `(($7ee >= 1) && ($7ee <= 5))`, `6…10`, … , `($7ee >= 25)` | `31 0, 70 0, 142, 2 7, 31 0, 70 1, 140, 0` | range tests on the game-message parameter `$7ee` (entry #25, message `0x…193`) |

The bucket expressions also confirm the comparison mapping of §3.4 against intent: op 142 is `>=` and op 140 is
`<=`. T023's `render_graph.py` prints ops 142/143 inverted (`<=`/`<`); its op 140/141 output is correct.

### 5.3 `0x028` node 181 (the gamemode-entity condition, T013)

```
#181 BooleanSwitch m_condition (typed):  30 1, 35, 31 0, 30 0, 144, 0
  m_configVars = [Identifier(0x0D80000000001A27), GetPvPEntity()]   m_dynamicVars = [$195]
  → (GetPvPEntity().$195 == Identifier(0x0D80000000001A27))
```

Step by step: `cv1` GetPvPEntity → ENTITY (the gamemode entity: first component 0x24 in the game EA);
op 35 pops it into `entity_ctx`; op 31 reads `$195` **in the gamemode entity's Statescript** (instance vfunc
`+0xE8` → SyncMgr vfunc `+0xD0`); `cv0` pushes INT64 `0x0D80000000001A27`; `==` is true only for an INT64 equal
to it. Expected:
- gamemode entity spawned and its `$195` = INT64 `0x0D8…1A27` → BYTE 1 → true subgraph; any other value → false
  (the graph's own conditions use `$195 == 0x0D8…15/16` elsewhere, so `$195` looks like a phase identifier).
- **no gamemode entity** (today's server): GetPvPEntity succeeds with the invalid entity id, op 35 stores it, and
  op 31 treats the invalid id as "no entity" (`entity_ctx == invalid` check) → reads the capture graph's **own**
  `$195` instead. So without a gamemode entity the switch follows the capture instance's own `$195`
  (the `0x…15/16` identifiers), not false. [G]
