# JAM — Blizzard's Network Serialization & Routing Library

Reference compiled from the GDC 2013 talk **"Network Serialization and Routing in World of Warcraft"** by Joe Rumsey (Principal Software Engineer, Blizzard; former lead server engineer on WoW).

- Source: the talk's slides, `JAMPresentationGDCFinal-JoeRumsey.pdf` (61 slides with speaker notes; where to find them: [gdc/README.md](gdc/README.md))
- Slide numbers are cited as `[S##]` so claims can be checked against the PDF.
- Everything in sections 1–10 comes from the talk. Section 11 connects it to Prometheus and the Overwatch binary; those links are **our hypotheses**, not statements from the talk.

> **Why this matters for Overwatch:** In 2013 Rumsey was working on "Blizzard's next MMO" (Titan, which later became Overwatch). He said that in current Blizzard projects JAM is "also used between game clients and servers" [S3]. The talk jokingly calls the game "World of Checkers", so every `Checker*` example below is a stand-in for real gameplay objects [S5, S61].

---

## 1. What JAM is

- **J**oe's **A**utomated **M**essages. It is an application-level network serialization and transport library used by Blizzard's MMOs [S2].
- Development started in **2001**, before protobuf, Thrift and Avro were available [S2, S57].
- When WoW shipped in 2004, JAM was used only **between game servers**. By 2013 it was also used by internal tools and, in current projects, **between game clients and servers** [S3].
- **There are two JAM implementations at Blizzard.** They share a common ancestor but not a source repository. WoW has features (for example memcpy serialization) that the "next MMO" branch had not picked up yet [S49].
- Design goals [S6]:
  - DRY (don't repeat yourself). Remove boilerplate to reduce bugs.
  - No hand-written serialize/deserialize code. (The talk opens with an off-by-one bug in hand-written serialization: `for(int i = values.size(); --i;)` [S4].)
  - Spend time on the game, not the protocol.
  - "Build a helpful robot that writes our code for us", meaning code generation.
- Target style: one plain struct per event, fill in its fields, call `Send()`. Handlers receive a typed pointer [S7].

## 2. Terminology [S22]

| Term | Meaning |
|---|---|
| **Message** | A serialized structure defined in a `.jam` file |
| **Protocol** | A collection of messages. In practice, one `.jam` file defines one protocol, and the file tells you the protocol's name [S10] |
| **Service** | A module of code that implements message handlers for one or more protocols |
| **Program** | A process. It can contain multiple services |
| **JamID** | A **128-bit** opaque address for either a *service* or an *object* [S20] |
| **JamLink** | An object representing a connection to a remote application [S31] |
| **JamRouteConfig** | Per-server object that maps protocols to handlers and dispatch policies [S37–38] |
| **Channel ID** | A 1-byte per-connection ID assigned to each negotiated protocol [S53] |

## 3. Development cycle [S9]

1. Describe the protocol (`.jam` file)
2. Generate serialization and dispatch code (`jamgen`)
3. Send messages
4. Receive messages
5. Configure routing info (`JamRouteConfig`)

For a new message inside an existing protocol, the only hand-written code is **the `.jam` definition, one sender function and one handler function**. Everything else is generated or written once per protocol [S35].

---

## 4. The `.jam` language

### 4.1 Message definitions [S10, S21]

The syntax deliberately looks like a C struct. Each `message` maps 1-to-1 to a generated C++ class with the same name and the same fields.

```c
// Checkers.jam
message CheckerCaptureCredit {
    CheckerID capturedCheckerID;
    CheckerID capturedBy;
    u8 jumpType;
};

message GroupUpdate {
    GroupID group;
    array<.CheckerID> checkers;     // dynamic array → vector<CheckerID> in C++
};
```

- Supported field kinds: primitive types (`u8`, `u32`, `u64`, ...), custom ID types, **dynamic arrays** (`array<.T>`, note the leading `.` in the slide syntax), **structs** and **arrays of structs** [S21].
- Fields can have **default values**. The generated no-arg constructor assigns them (`f.hasDefault` / `f.defValue` in the template) [S16].
- **Message IDs are assigned by the order messages appear in the `.jam` file** [S48].
- "Struct" support:
  - Legacy: any C++ class that implements hand-written serialization methods. This still works for C++-only protocols [S21].
  - Current: **`structgen`**, a separate reflection/codegen tool for non-message data. It is also used by the database and asset layers, which is why it is separate from jamgen. It is built on TRL like jamgen. Structgen structs, and arrays of them, can be embedded in JAM messages [S21].
- Special types can be built into jamgen itself (for example `CheckerID` as a typedef to `uint8`, later widened to `u16` for every protocol at once). The talk calls this contrived: they "don't generally add special types to jamgen itself" [S18].

### 4.2 Grammar (excerpt of `jam.y`, Bison) [S14]

```yacc
jamstructs   : jamstructs jamstruct
             | jamstruct
             ;

jamstruct    : structtype TIDENTIFIER messagename '{' { jam_start_s... }   /* truncated on slide */
             | structtype TIDENTIFIER messagename '{' '}' ';' { jam...     /* empty body */
             | jamcomment { /*printf("Comment\n");*/ }
             ;

structtype   : TMESSAGE       { $$.type = GMESSAGE; $$.qualifiers = 0; }
             | TOBJECTMESSAGE { $$.type = GMESSAGE; $$.qualifiers = ... }  /* truncated */
             | TSTRUCT        { $$.type = GSTRUCT;  $$.qualifiers = 0; }
             ;
```

What the grammar tells us:
- There are **three top-level declaration keywords**: `message`, `objectmessage` and `struct`.
- `objectmessage` is a `GMESSAGE` with a **qualifier**. This matches the wire-level split between *service messages* and *object messages* (§8.3).
- A declaration is `structtype TIDENTIFIER messagename { ... }`. There is an extra identifier token between the keyword and the name, whose purpose is not explained. Empty-bodied messages are allowed.
- Comments are grammar-level tokens.

### 4.3 Toolchain [S13–S17]

- Parser: **Flex + Bison** (the talk also mentions ANTLR, GOLD, PLY and Boost.Spirit as alternatives).
- Parsing a `.jam` file produces a syntax tree of message metadata: fields, names, types (as strings *and* enum values) and other attributes.
- The compiled parser program is **`jamgen`**.
- Code generation pipeline [S15]:

  ```
  2004:  .jam → Parser → many fprintf()s → .cpp
  2013:  .jam → Parser → TRL template dict → TRL processor → .cpp
  ```

- **TRL (Template Replacement Language)** is Blizzard's in-house template engine, inspired by Django templates (comparable to CTemplate, ngTemplate and Jinja). jamgen walks the syntax tree, fills a string-keyed dictionary (a `messages` list, each with a name, metadata and a `fields` list), and hands the dictionary and a `.trl` file to the TRL processor [S16–S17].
- TRL syntax: `{{ expr }}` substitution, `{@ foreach x in list @}…{@ end foreach @}`, `{@ if cond @}…{@ end if @}`, and `{@ define Name(args) @}` macros. It has no function calls [S16–S17].

```
{@ define OutputMessage(msg, encoders, decoders) @}
    //
    // NOTICE: This is generated code. DO NOT EDIT!
    //
    class {{ msg.structName }} : public JamMessage {
    public:
        static u32 CRC;
        static u16 CODE;
        static cchar *NAME;

        // No argument constructor:
        {{ msg.structName }}() {
            {@ foreach f in msg.fields @}
                {@ if f.hasDefault @}
                    {{ f.name }} = {{ f.defValue }};
                {@ end if @}
            {@ end foreach @}
        }
```

- Because all serializers come from templates, a single template edit changes **every protocol and message** at once. The real example shown was adding message compression, which was a two-line diff to the TRL file [S18].

---

## 5. Generated code

### 5.1 Message class [S10, S16]

```cpp
// 100% generated, e.g. JamCheckers.cpp
class CheckerCaptureCredit : public JamMessage {
public:
    static u32 CRC;         // per-message CRC
    static u16 CODE;        // message ID within the protocol (file order)
    static cchar *NAME;     // message name string

    // Message decoders
    BOOL Get(BinaryDecoder &decoder);
    BOOL Get(JSONDecoder &decoder);
    // Message encoders
    BOOL Put(BinaryEncoder &encoder) const;
    BOOL Put(JSONEncoder &encoder) const;

    /**** DATA START ****/
    CheckerID capturedCheckerID;
    CheckerID capturedBy;
    u8 jumpType;
    /**** DATA STOP ****/
    // Lots more stuff...
};
```

- `JamMessage` is an **interface base class with virtual methods** for serialization and other generic operations [S10].
- There is a generated **static `GetMessageID`** method, used to type-check responses (§6.3) [S25].
- Dynamic arrays become a vector type in the data section. The talk shows `vector<CheckerID>`, but Blizzard uses **its own container classes, not the STL** [S21].
- The data members sit between `DATA START` / `DATA STOP` markers and use plain value types. Messages **contain no pointers to other objects** [S45].

### 5.2 Serializer [S12]

```cpp
//NOTICE: This is generated code. DO NOT EDIT!
BOOL CheckerCaptureCredit::Put(BinaryEncoder &_encoder) const
{
    _encoder.BeginMessage(CODE, NAME);
    _encoder.Put("capturedCheckerID", capturedCheckerID);
    _encoder.Put("capturedBy", capturedBy);
    _encoder.Put("jumpType", jumpType);
    _encoder.EndMessage(CODE, NAME);
    return TRUE;
}
```

- The encoder is called once per field **with the field name as a string**. The binary encoder ignores the name, and the talk calls it "completely useless" there. The JSON encoder uses it [S50].
- An encoder is basically a stream: the message serializes itself into the encoder, and a transport layer ships the bytes. The encoder internals and the transport are out of the talk's scope [S12].

### 5.3 Virtual encode dispatcher (real template diff) [S18]

This screenshot of a real TRL template shows internal names with Blizzard's **`te` prefix**:

```cpp
// Virtual encode/decode dispatchers
BOOL {{m.structName}}::Put(teDataChain *pPayload, ... protocolType ...)
{
    switch(protocolType) {
        default:
            return FALSE;
        case JAM_PROTOCOL_BINARY_COMPRESSED:          // added by the diff
        case JAM_PROTOCOL_BINARY_LITERAL:
        {
            teRawBinaryEncoder encoder(pPayload);
            return Put(encoder);
        }
        case JAM_PROTOCOL_TEXT_JSON_COMPRES...:       // added by the diff (name truncated on slide)
        case JAM_PROTOCOL_TEXT_JSON:
        {
            teJSONEncoder encoder(pPayload);
            return Put(encoder);
        }
    }
}
```

- **Serialization modes enum:** `JAM_PROTOCOL_BINARY_LITERAL`, `JAM_PROTOCOL_BINARY_COMPRESSED`, `JAM_PROTOCOL_TEXT_JSON`, `JAM_PROTOCOL_TEXT_JSON_COMPRES…` (probably `_COMPRESSED`).
- Compression is applied to the payload buffer (`teDataChain`) outside the encoder, which is why the compressed cases fall through to the same encoder.
- Class names: `teDataChain` (payload buffer), `teRawBinaryEncoder`, `teJSONEncoder`.

### 5.4 Dispatcher [S32]

```cpp
//NOTICE: This is generated code. DO NOT EDIT!
template<typename HANDLER_T>
static JAM_RESULT Dispatch(JamMessage *pMessage, HANDLER_T *pHandler) {
    switch(pMessage->GetCode()) {
    case JAM_MSG_CheckerHeal:
        result = pHandler->CheckerHealHandler(link, (CheckerHeal *)pMessage);
        break;
    // cases for rest of protocol's messages...
```

- Generated per protocol as a static template, for example `JamCheckerProtocol::Dispatch<Checker>(msg, obj)`.
- The switch runs on the message **code** and uses `JAM_MSG_<MessageName>` constants.
- It calls `<MessageName>Handler(JamLink&, <MessageName>*)` on the handler object. This is **not virtual**; it is resolved through templates.

### 5.5 Handler prototypes [S33]

jamgen writes a header that contains only prototypes. You `#include` it **in the middle of your own class declaration**:

```cpp
JAM_RESULT CheckerHealHandler(JamLink &link, CheckerHeal *msg);
JAM_RESULT CheckerDamageHandler(JamLink &link, CheckerDamage *msg);
JAM_RESULT CheckerPowerupHandler(JamLink &link, CheckerPowerup *msg);
JAM_RESULT CheckerKingHandler(JamLink &link, CheckerKing *msg);
```

- Older versions generated **pure-virtual interfaces** instead, which meant lots of multiple inheritance and virtual-call overhead. The talk says both approaches work.
- Handlers return `JAM_RESULT`; success is `JAM_OK` [S34].

---

## 6. Sending

### 6.1 Basic send [S20]

```cpp
void Checker::OnCaptured(CheckerID capturedBy, JUMP_TYPE how)
{
    CheckerCapturedCredit msg;               // usually a stack local
    msg.capturedCheckerID = GetID();
    msg.capturedBy = capturedBy;
    msg.jumpType = how;
    JamID destination = GetRouter()->GetCreditManagerID();
    GetRouter()->Send(destination, &msg);
}
```

Messages are ordinary objects: stack, `new` or pools all work. A few high-volume paths avoid the construct/destruct cost, but 99% of sends look like this.

### 6.2 Destination kinds [S23–S24]

| Call | Target |
|---|---|
| `Send(JamID serviceID, &msg)` | A specific, known, connected service |
| `Send(JAM_SERVER_<TYPE>, &msg)` | **Any** connected service of that type. **Queued** if none is available yet or the connection was lost, and flushed when a service of that type links up (a "poor man's message queue"). They were considering AMQP [S24] |
| `Broadcast(JAM_SERVER_<TYPE>, &msg)` | All connected services of that type |
| `Send(ObjectID, &msg)` | A specific **object** inside a service. Object IDs (for example `CheckerID`) are also 128-bit and interchangeable with JamIDs as destinations, as long as the receiver's dispatcher can resolve them [S23] |

- `JAM_SERVER_*` is an application-type enum. The program-type enum in 2013 had **45 entries**, one per application, plus a per-application **subtype** [S39].
- JAM provides **link up / link down notifications**, which carry the peer's JamID [S24].

### 6.3 Request/response ("registered messages") [S25]

```cpp
m_pService->SendRegistered<PlayerAdded>(JAM_SERVER_MATCHMAKER, &addMsg);
// PlayerAddedHandler is called with the response when it arrives
```

This is not full RPC. JAM inserts an **integer sequence ID into the message header**, and the responder replies with the same sequence. On receipt, the response type is checked against the template argument using the generated static `GetMessageID`.

### 6.4 Object messages and routing [S26–S28]

- Every object is **owned by exactly one server**. Read-only **mirrored** copies exist on nearby servers. Objects interact **only by sending each other messages**.
- Each object stores its owner: `JamID m_serverID` (for a local object, this is the local server's ID).
- **Loopback is allowed**, so the same code path works for local and remote objects.
- Services with objects write one routing helper:

```cpp
void BoardServer::Send(Checker *pChecker, JamMessage *pMessage)
{
    m_pJamServer->Send(pChecker->GetServer(),   // owning server JamID
                       pChecker->GetID(),       // 128-bit object ID carried in the header
                       pMessage);
}
```

---

## 7. Receiving

### 7.1 Object dispatch (hand-written, once per service) [S30]

```cpp
// static callback registered with JAM by protocol ID, called for each incoming message
void BoardServer::CheckerDispatch(JamLink &link, JamMessage *pMessage)
{
    CheckerID destID = pMessage->GetDestination();
    Checker *pChecker = GetCheckerObject(destID);
    pChecker->QueueMessage(pMessage);
    switch(pMessage->GetProtocolCRC()) {
        case JAMCheckerProtocol_CRC:
            JamCheckerProtocol::Dispatch<Checker>(pMessage, pChecker);
    }
}
```

- `JamMessage` exposes `GetDestination()`, `GetProtocolCRC()` and `GetCode()`.
- Protocols are identified by **CRC** (constant `JAM<Name>Protocol_CRC`).
- Only object-routed protocols need this code. The real gameplay server routes messages **to components based on the protocol**, not just to base objects [S30].

### 7.2 JamLink [S31, S39]

A connection to a remote application. It exposes:
- the peer's **application type**, **subtype**, and context such as map ID ("board ID")
- the protocols the peer supports
- the peer's unique **JamID**
- `Describe()`, which returns a human-readable string, and the ability to **disconnect** a misbehaving peer

You can look up a JamLink from a JamID at any time, but it is only guaranteed valid **inside a handler**, for the sender of that message.

### 7.3 Handler [S34]

```cpp
JAM_RESULT Checker::CheckerHealHandler(CheckerHeal *pMessage)
{
    m_health += pMessage->amount;
    LOG("Checker %d was healed for %d by checker %d",
        GetID(), pMessage->amount, pMessage->healedBy);
    return JAM_OK;
}
```

### 7.4 Message lifetime [S45]

- Incoming messages are **refcounted**. A handler can keep a message for later and smart pointers are provided.
- Messages contain no pointers, so reference cycles are impossible.
- Some services build pending queues by simply holding references to unprocessed messages.

---

## 8. Routing configuration, threading, wire format

### 8.1 JamRouteConfig [S37–S38]

```cpp
void Matchmaker::Configure(JamServer *pServer)
{
    JamRouteConfig &routeConfig = pServer->GetRouteConfig();
    routeConfig.ConfigureInbound<MatchmakerProtocol>(this, Matchmaker::DispatchMessage);
    routeConfig.ConfigureOutbound<MatchmakerResponseProtocol>();
}
```

- `ConfigureInbound<Protocol>(handlerObj, dispatch|queue)` declares that this service **receives** the protocol. `ConfigureOutbound<Protocol>()` declares that it **sends** it.
- The handler object can be any instance that implements the protocol's handlers, not necessarily the service itself.
- For each mapping, RouteConfig creates a **templated dispatcher object** (no virtual interfaces). When a message arrives it resolves: which protocol → which handler → which dispatch policy.
- A flag on RouteConfig enforces **"matching protocols only"**, which is used for the **game client**. If negotiation finds no matching protocols, the connection is refused [S53].

### 8.2 Threading / dispatch models [S40–S44]

| Model | Behaviour |
|---|---|
| **Default (raw)** | Handlers are called **concurrently from multiple network threads** with no synchronization. The only guarantee is that messages from the *same sender* are never processed concurrently. The talk calls this "the scary option" [S40, S43] |
| **Message queue** (preferred) | `ConfigureInbound<P>(this, &m_messageQueue)`, then `pServer->ProcessQueue(&m_messageQueue, this)` at a known time on one thread, for example in `Idle()`. **This is what the game client uses** [S41] |
| **Global lock** | RouteConfig acquires and releases one service-wide mutex around each handler. Effectively single-threaded, but avoids message copies [S42] |
| **Lock policy** | A template parameter with `Lock(JamMessage*, JamMessageQueue**)` and `Unlock(JamMessage*)`, with no base class. It can choose read or write locks per message code, or defer messages into a queue. The global lock is just one lock policy [S44] |

`JamMessageQueue` is a linked list with internal locking. Producers enqueue from any thread, and `ProcessQueue` swaps it into a local list for processing [S41].

```cpp
class MatchmakerLockPolicy {
    Matchmaker *m_owner;
    void Lock(JamMessage *msg, JamMessageQueue **ppQueue) {
        if (msg->GetCode() == JAM_MSG_MatchmakerAddPlayer) m_owner->AcquireWriteLock();
        else                                              m_owner->AcquireReadLock();
    }
    void Unlock(JamMessage *msg) { /* same logic, release */ }
};
```

### 8.3 Protocol negotiation (connection handshake) [S48, S53]

1. On connect, **each side sends a "Hello"** listing every protocol it supports: **name**, **CRC**, and direction (send, receive or both).
2. Each peer checks, for every protocol, whether one side is willing to send it and the other to receive it.
3. Each matched protocol gets a **1-byte channel ID**, assigned deterministically **by the order the protocols appear in the metadata**. Whether a side is the **listener or the connector** decides whose ordering takes precedence. This limits a connection to **256 active protocols**.
4. From then on, every message carries the channel ID instead of a protocol identifier.

**Protocol CRC:** jamgen computes a CRC per protocol over the messages (in order) and their fields. Identical CRCs mean the two sides have exactly the same binary layout, so they use raw binary. A mismatch switches that protocol to **JSON** (§9).

### 8.4 Wire format [S54]

```
+----------------------+
| Size          1–3 B  |  variable-length size
| Channel ID    1 B    |  negotiated protocol (§8.3)
| Message Type  1 B    |  service msg / object msg / internal (negotiation, ping, conn mgmt)
| Message ID    2 B    |  message index within the protocol (.jam file order)
| [Object ID   16 B]   |  only for object messages (128-bit)
| Payload              |  binary fields, or JSON
+----------------------+
```

- Header overhead is **5–7 bytes** for service messages, plus 16 for object messages.
- **Object messages are never used on public-facing protocols**, only between internal servers. Client↔server traffic should therefore always be service messages.
- The registered-message **sequence ID** (§6.3) is also carried "in the message header". The slide does not show where.
- Binary payload: fields in declaration order with no names or tags. As of 2013 there was **no varint or bit-packing** (Rumsey was considering it) [S59]. Generated code makes it possible to add per-message or per-protocol bit-packing and direct buffer writes later [S50].
- **WoW only:** when jamgen detects that the struct layout matches the wire format and negotiation confirms the endianness matches, the message is simply **`memcpy`'d onto the wire** [S49].

---

## 9. Versioning: JSON fallback [S47–S52, S55–S56]

- 2004: JAM assumed binary compatibility, since all servers of a build were deployed together. There was no cross-version support at all [S47].
- Later, a per-protocol CRC mismatch switches that protocol to **JSON-encoded JAM** (name=value pairs). Missing fields take their defaults, unknown fields are ignored, and some type changes are tolerated [S51].
- JSON is **never allowed on public-facing protocols** and is sometimes too expensive even internally [S51].
- Example JSON message (`_msgID` is the message ID) [S52]:

```json
{
    "_msgID":10,
    "type":6,
    "error":0,
    "desc":{
        "m_id":"T2R00S40.00E14815726P10987H127.0.0.1:14001",
        "m_host":"127.0.0.1",
        "m_partitionID":0,
        "m_configID":0,
        "m_buildNum":0,
        "m_type":40,
        "m_subType":0
    }
}
```

  Notes on the sample:
  - `desc` looks like a **service descriptor struct** with `m_`-prefixed fields.
  - `m_type` (40) and `m_subType` are the application type and subtype (§7.2).
  - The string form of `m_id` seems to encode `T`ype 2, `R`, `S`ubtype/service 40.00, `E`poch/timestamp, `P`id 10987 and `H`ost:port. This decoding is our guess.

- Wire size in bytes, including overhead [S55]:

| Payload | Binary | JSON |
|---|---|---|
| Two 32-bit ints | 13 | 49 |
| u32 + vec3 | 21 | 75 |

  Two u32s give 8 B payload + 5 B header = 13 B, which is consistent with the header layout in §8.4.

- Operational benefit: tools keep working against older or newer back-ends without synchronized deploys. Around 11 builds a day at the time; a build picker screenshot shows "Blessed (8939)" and "Latest Build (8967)" [S56].

## 10. JAM vs. alternatives [S57–S60]

- Protobuf, Thrift and Avro offer compact binary **plus** cross-version compatibility. JAM prioritized speed with exact version matching [S58].
- Benchmarks [S59]:
  - Protobuf's default C++ implementation was **3–5× slower** than JAM at serializing and deserializing in almost every case. Timed ops: `ConstructRequest`, `SerializeOpenDirToString`, `SerializeOpenDirToArray`, `DeserializeOpenDirRequest`, `SerializeDirContents`. Approximate chart values for `SerializeDirContents`: protobuf ~32,000 ms vs JAM ~8,500 ms.
  - Size: protobuf wins on small values thanks to varints (`SmallStatFS`: ~22 B vs ~66 B). JAM wins on the large case (`LargeStatFS`: protobuf ~91 B vs JAM ~66 B). JAM's size is constant because it does no integer packing.

---

## 11. Relevance to Prometheus / the Overwatch 0.8 binary (hypotheses)

Links between the talk and what Prometheus already knows. **Verify each one in Ghidra before relying on it.**

| Talk concept | Prometheus / binary counterpart | Notes |
|---|---|---|
| `JamMessage` virtual base class | `JamMessage { JamMessageVTable* vfptr; }` in `prometheus/Prometheus-Core/JAM.h` | vtable has `get_message_info` and `clone_instance` |
| Generated statics `CRC`, `CODE`, `NAME` + protocol name | `JamMessageInfo { message_name, message_protocol, items, crc, message_id }` | In OW these appear to be packed into one static metadata record per message |
| Field names passed to `_encoder.Put("name", …)` (needed for JSON) | `JamMessageItemInfo { name_str, message_type, offset, size, has_extra_flag, inner_data_ptr, decoding_func }` | OW looks **table-driven** (field descriptors with offset/size) rather than one `Put` call per field. This may be the "smarter" generated serializer the talk hinted at [S50]. `inner_data_ptr` probably describes nested structs and arrays (structgen) |
| Protocol = `.jam` file with CRC; message IDs in file order | `JamPoolVtable { pool_name, pool_crc, min_msgId, max_msgId, allocate_pool(msgId), … }` | A "pool" is probably one **protocol**: name, CRC, message ID range, plus a factory that creates a message by ID for decoding |
| `RouteConfig::ConfigureInbound<Protocol>(handler, …)` | `associatePoolHook(associationInfo{crc, name}, receiverInstance)` at `gameBase+0xf25ba0` | Associates a protocol CRC with a receiver or dispatcher, which matches the protocol→handler mapping |
| Protocol registration list | `addJamProtocolHook(linkedList, dispatchInfo)` at `gameBase+0x8a27d0` | Probably builds the local protocol list sent in the **Hello** handshake |
| Generated `Dispatch<HANDLER_T>` switch on `GetCode()` | GameEntityAdmin's "JAM message virtual function table" | OW may have switched back to virtual handler tables, which the talk describes as the older style [S33] |
| `te*` class prefix (`teDataChain`, `teRawBinaryEncoder`, `teJSONEncoder`) | `teList<T>` in `globals.h` | Same Blizzard engine naming. Search Ghidra strings and RTTI for `te*Encoder`, `teDataChain`, `JAM_PROTOCOL_*` |
| Message queue model is the one used by the game client [S41] | — | Look for a per-frame `ProcessQueue`-style drain of a locked linked list on the main thread |
| ReplayEntityAdmin "disabled JAM sending" | — | Consistent with sending going through a per-admin router/service object |

### RE search leads

- **Strings:** `JAM_`, `Jam`, `JSON`, `_msgID`, protocol names, `Handler` suffixes, `DO NOT EDIT`, `m_subType`, `m_partitionID`, `m_buildNum`. The JSON codepath needs field names at runtime, which is presumably why `JamMessageItemInfo::name_str` exists.
- **Handshake:** a "Hello" message holding (name, CRC, direction) tuples, and a channel table indexed by a `u8`.
- **Framing:** a 1–3 byte varlen size, then `u8 channel`, `u8 type`, `u16 msgId`, then an optional 16-byte object ID. When parsing captured client↔server traffic, expect service messages only, since object messages are internal-only.
- **Compression:** the `JAM_PROTOCOL_BINARY_COMPRESSED` mode means some channels or messages may carry compressed payloads.
- **Response sequence:** registered messages carry an integer sequence in the header. Look for request/response pairing tables keyed by sequence and expected message ID.
- **Caveat:** the talk describes the 2013 state of the *Titan-era* fork. Overwatch 0.8 (2015) will differ in details. For example, OW appears table-driven where the talk shows per-field `Put` calls.

---

## 12. Our implementation (T049)

The servers' JAM layer is generated from the client's own protocol tables, the way jamgen does it at
Blizzard (§3): one catalogue, one generator, typed messages. Code: `server/inst/jam` (package `jam`),
`server/jamgen` (generator), catalogue `systems/data/jam/catalogue.json`.

### 12.1 What the client tells us [G]

- **Field tables.** Each message has a table of `JamMessageItemInfo` entries (0x28 bytes: name, type,
  offset in the message object, size, array flag, nested table, func), ended by type 17. Prometheus'
  `jam_protocol_dump` exported all of them to `game/owbetaclient/Prometheus/jam_protocols.json`: 46 protocols,
  348 messages. Message and field names are stripped in the lobby/instance protocols; the tool protocols
  (`Command`, `CommandOut`, `Log`, `LogControl`) and a few fields (`seq`/`src`, `x/y/z/w`, `id`, `m_*`) keep theirs.
  The negotiation protocol `0x11D82194` is registered before the hook; its layout is in §12.5.
- **Type numbers and signedness.** The JSON encoder (`FUN_7ff650f0ce90`, a jump table on the type) formats
  each type with its own `printf` conversion, which settles the signedness the binary decoder can't show:

  | type | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 |
  |---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
  | wire | bool | char `%c` | u8 `%u` | i8 `%d` | u16 `%u` | i16 `%d` | u32 `%u` | i32 `%d` | u64 `%llu` | i64 `%lld` | f32 | f64 | str | blob | struct | bytes | tid |

  This corrects the guess in TitleScreen.md §5 "Conventions" (it printed 2/3 both as u8, 4 as i16 and 5 as
  u16): the right mapping is 4 = u16, 5 = i16. Example: the port in 20600 is a u16.
- **Binary decoding** (`FUN_7ff650f0c7d0`, table walker `FUN_7ff650f0ce00`). Fields come in table order,
  little-endian, packed. A bool is one bit, LSB first, and consecutive bools share a byte, also across
  nested-struct boundaries. Every other read clears the bit counter, so the next field starts on a whole
  byte. str = NUL-terminated (`FUN_7ff650e29660` scans for the NUL within the bytes left, so the Prometheus
  dump's label "rest of the message (blob)" is wrong). bytes = u32 length + bytes. blob = `size` bytes.
  tid = u8 kind 1..5 + 1/2/4/8/16 bytes. Arrays: flag -1 = u32 count + elements, n > 0 = n elements.
- Put another way, a JAM payload is **a bit stream in which every non-bool field aligns to a byte**. The
  same code therefore reads a message in a lobby frame (padded to a byte) and inside the instance data
  payload, where a message's trailing bools are bits of the stream and the next header aligns again
  (server/internal/instance/stream.go found this empirically [L]).
- **Encoding** (`FUN_7ff650f0e410`): mode 1 = binary: the message index byte, then the fields
  (`FUN_7ff650f0e980`); mode 2 = JSON with `"_msgID"` (`FUN_7ff650e2dae0`), which the game client never
  negotiates.

### 12.2 The catalogue (`systems/data/jam/catalogue.json`)

One JSON file holds every protocol the client registers. For each protocol it has the name, CRC,
`base..max-1`, direction (client's view: `in` / `out`), transport (`lobby`, `instance`, or none for dev
tools), server type (13 = lobby), instance channel, client receiver/dispatcher RVAs and notes. For each
message it has the ID, the Go name, a label, the client handler RVA, `use` (what our servers do: `send` /
`recv`), provenance tags (G/L/K/H) and notes, and the fields with name, type, array, size and the offset in
the client's message object. **22 shared types** replace the anonymous structs they match: `JamID {seq,
src}`, `EntityID`, `Vec3/4`, `ChatChannel`, `ChatUser`, `ChatChannelState`, `Unlock`, `HeroRecord`,
`PartyMember`, `GameID`, `GameInfo`, `CustomGameSettings`, `MatchmakingTicket`, `QueueStats`,
`QueueEntry`, `QueueStatus`, `StatList`, `FriendEntry`, `FriendRequest`, `PresenceRecord`, `Currency`.

| transport | protocols | messages | named | used by our servers | client handler known |
|---|---|---|---|---|---|
| lobby (incl. Negotiation) | 33 | 225 | 136 | 53 | 76 |
| instance stream | 8 | 101 | 15 | 7 | 4 |
| dev tools (Command, Log, …) | 6 | 26 | 24 (client names) | 0 | 0 |

Every layout comes from the client. Names, handlers and notes were collected from TitleScreen.md §5,
LobbyServer.md, MatchLogic.md, the early lobby notes (§12.5) and the R&D servers' encoders. Unnamed messages get
`<Protocol><ID>` (e.g. `ClientOutLobbyParty22105`) and unnamed fields `F<i>`.

**Editing.** Edit the JSON by hand: give names, add notes, or introduce a shared type by replacing an
anonymous `struct` with the type's name. Then run `go generate ./inst/jam` (from `server/`).
`go run ./jamgen seed` rebuilds the layouts from a new Prometheus dump and keeps every curated attribute
wherever the layout still matches. A shared type is kept when its shape still matches, and a changed
layout is reported. `go run ./jamgen check` validates the file: ID ranges, unique Go names, known types.

### 12.3 The `jam` package

| file | what |
|---|---|
| `messages_gen.go` | generated: one struct per message and per nested struct, `Encode(*bits.Writer)` / `Decode(*bits.Reader) error`, `ID*` constants, `New(id)`, the protocol table |
| `codec.go` | the wire primitives above (put/get per type, bool packing via `bits`, count sanity checks), `JamID`, `EntityID`, `Vec3/4`, `TaggedID` |
| `jam.go` | `Message`, `Raw` (uncatalogued / pass-through), `Protocol` + lookups, `Marshal`/`Unmarshal` (a whole lobby payload; trailing bytes are an error), `LobbyChannels()`, a typed `Dispatcher` (`jam.On(&d, func(c *Conn, m *jam.HeroLockIn) error {…})`) |
| `lobby.go` | lobby framing `[u24 BE len][u8 ch][u8 idx][payload]`; `LobbyLink` = the negotiated channel table (channel 0 = Negotiation, 1..n = the client's `NegotiateProtocols` CRC list, i.e. the type-13 protocols sorted by CRC) |
| `stream.go` | instance data payload stream 0: `WriteStream` / `ReadStream` of `Envelope{Reliable, Seq, Msg}` (present bit, u4 reliable / u4 unreliable counts, per message `[u32 seq] u8 ch u8 idx payload`), `InstanceChannelOf` / `InstanceMessageID` (the 8-protocol stream list) |

Bit I/O is `server/inst/bits` (T048). The lobby stream cipher and handshake stay in `server/internal/lobby`.
Sequencing and resends of reliable instance messages belong to the link (`server/inst/link`, T048).

**Tests** (`go test ./inst/jam`) cover four things. (1) Every one of the 352 messages round-trips with
random values, both alone and packed several to a bit stream at an unaligned start. (2) Every lobby payload
in `testdata/lobby_traffic.txt` (149 distinct send/recv records from the R&D servers' log, including the
client's 147-byte `LobbyConnect`) decodes with no byte left over and re-encodes to the same bytes.
(3) Every client instance-stream message in `testdata/instance_traffic.txt` (50) decodes and ends inside the
logged bytes. (4) Bool packing, the channel tables, and the decode errors: a string without NUL, an
impossible count, a short payload, trailing bytes.

### 12.4 The serialization boundary

Some payloads use **the client's memory layout**. Component mirror updates are written through STU
reflection, and there are also Statescript full/delta updates and movement snapshots. We produce these
with the C++ libraries behind their C APIs (`systems/stu`, `systems/statescript`, and later
`systems/replication`), not with generated JAM code. On this build they are **not JAM messages**: they
travel in streams 1/2/4 of the instance data payload, next to the JAM stream 0, and their byte alignment is
relative to the packet (e.g. the component updates of a stream-4 create). The boundary is therefore a
serializer that writes **into the packet's `*bits.Writer` at its position**. A C/C++ implementation takes
the start bit phase (`w.Len() % 8`), returns bits + bit count, and the Go side appends them. The
interfaces are in `server/inst/replication/serializer.go` (T048): `MirrorSerializer` (component mirror
updates: FilterBits 0x10, possession 0x20, health 0x28, PlayerInfo 0x3F → `ComponentUpdate{ID, Mask,
Values}`), `ScriptSerializer` (hero start-up and remote full updates) and `SnapshotSerializer` (movement
snapshots written in place at the packet position), grouped as `Serializers`. `Stopgap` implements all
three with the R&D server's encoders (tested byte-equal to them) and is `replication.Default`; a match
picks its serializers through `match.Config.Serializers`, so C++-backed implementations drop in there. JAM carries opaque bytes
only where the client itself declares a `bytes`/`blob` field (e.g. `ClientOutInstance` 21614,
`CustomGameSettingsPrint` 23309, the presence attributes of 27109).

### 12.5 The lobby link: frames, negotiation protocol, link state machine (from the early notes) [G, L]

Read from the client while getting the first lobby connection up; confirmed live up to the title screen.

**Frame** (after the lobby handshake, inside the stream cipher):
`[u24 BE length][u8 channel][header fields per channel flags][u8 message index][payload]`.
- `length` covers everything after it; it is checked against a per-link maximum (`+0x88`) in `FUN_7ff650f091f0`.
- `channel` indexes the link's channel table (`+0xA0`, count `+0xA8`); out of range = link error.
- Header fields (`FUN_7ff650f0a090`) come from the channel's flags (§13.2); none are set for the stock lobby
  protocols, so the frame is the plain `[u24 len][u8 ch][u8 index][payload]` our gateway writes.
- **Message ID = index + the protocol's base ID** (`FUN_7ff650f0e5d0`: `pool->minId()`, then
  `pool->allocate(id)`); an ID the pool can't allocate drops the link. Unlike the talk (§8.4) there is no separate
  message-type byte and the size is a fixed u24, not a 1–3-byte varlen.
- Each channel has a mode (`+0x1`): 1 = binary, 2 = JSON (`"_msgID"`, §9), 0 = unusable. Channels created for
  matching CRCs are binary.

**The negotiation protocol `0x11D82194`** (channel 0) is registered by `FUN_7ff650f0b3e0` as `{crc 0x11D82194,
base 1, max 5, server type 0}`, before Prometheus' dump hook, so its layout was read from the static tables in
`.data` (entries `{name, protocol, fields, (msg << 32) | crc}` at `0x7ff65170cac8…0x7ff65170cc40`):

| msg | fields | handler (dispatch `FUN_7ff650f0b2f0`) |
|---|---|---|
| 1 | `u32[]` protocol CRCs | `FUN_7ff650f0ac90` (requires link state 2) |
| 2 | `{seq u64, src u64}` (a JamID) + `[]{m_protocolCRC u32, m_messageBase u32, m_messageMax u32, m_protocolFlags u32, m_untrustedServer u32, m_protocolName cstring}` | `FUN_7ff650f0ade0` (only when the link's mode flag ≠ 0) |
| 3 | empty | `FUN_7ff650f0afe0` |
| 4 | empty: keepalive, sent by the client every 10 s | ignored |

Any other message on channel 0 closes the link.

**Link state machine.** A JamLink has a state at `+0x18` and a mode flag at `+0x82`. For the game client the
flag is **0** ("matching protocols only", the talk's client mode; the descriptor message 2 belongs to the other
mode, with the JSON fallback).

```
client: FUN_7ff650f0a600 filters the route entries by the peer's server type, sorts them by CRC, creates channels 1..n
C→S  msg 1 [sorted CRC list]     on channel 0; client link state → 3
S→C  msg 3 (empty)               FUN_7ff650f0afe0: flag 0 and state 3 → state 5 (LINK UP), event 0
```

- **Do not echo message 1 back.** Its handler requires state 2 and closes the link in state 3 (seen with
  Prometheus' `JamDiagnostics` hook: close from RVA `0xF2ACD4`, "event 2 reason 3"). `FUN_7ff650f0a2d0` would
  validate an echoed list as "identical to mine", but that path is for the other side's state.
- The server type is bits 40–55 of the server JamID in the handshake trailer
  ([early-notes/03](early-notes/03-lobby-handshake.md)). With type 0 the client's list is only `[0x11D82194]`
  and no lobby protocol gets a channel, so every content message is fatal.
- Link events (`FUN_7ff650f09510`): 0 = up, 1 = down while connected, 2/3 = down or error during negotiation.
  Every JAM-level close goes through `JamLink::Close` (`FUN_7ff650f088f0`).
- `FUN_7ff650f0a960` (message 2 path) matches a descriptor by CRC (→ binary), or by name when both have flag
  `0x4` and the CRCs differ (→ JSON); unknown → a mode-0 channel. `FUN_7ff650f08a20` stores message 2's
  `{seq, src}` as the peer JamID at link `+0x20`.

**The lobby channels** with a type-13 server (CRC order; IDs are `base..max-1`):

| ch | protocol | CRC | IDs | ch | protocol | CRC | IDs |
|---|---|---|---|---|---|---|---|
| 1 | ClientOutLobbyFriends | 027adb37 | 27000–27009 | 17 | ClientInLobbyCustomGame | 74c43f11 | 23300–23313 |
| 2 | ClientOutLobbyCustomGame | 0af5e1b5 | 24000–24019 | 18 | ClientInLobbyChallenge | 75e8ed61 | 28200–28203 |
| 3 | ClientInLobbyStats | 0d111732 | 27200–27203 | 19 | ClientInLobbyStore | 7845d37e | 26400–26410 |
| 4 | ClientOutLobbyChat | 11757702 | 21700–21707 | 20 | ClientInLobbyRelationship | 8a89bc90 | 31800–31805 |
| 5 | ClientOutLobbyStats | 2baf715d | 27300–27303 | 21 | ClientInLobbyPlayer | 912cea67 | 20800–20806 |
| 6 | ClientOutLobbyParty | 3bf03c3b | 22100–22110 | 22 | ClientOutLobbyDebugCalendar | 91acc056 | 30600–30602 |
| 7 | ClientOutLobbyChallenge | 40bf3b01 | 28300–28306 | 23 | ClientOutLobbyHeroProgression | 9f0647cd | 24500–24509 |
| 8 | ClientOutLobbyPersistence | 4116e22c | 26200–26201 | 24 | ClientOutLobbyPlayer | b7c5277c | 22200–22211 |
| 9 | ClientInLobbyAchievement | 42d8c448 | 28000–28003 | 25 | ClientOutLobbyStore | b9c48546 | 26500–26508 |
| 10 | ClientInLobbyConnect | 4e958daf | 20500–20506 | 26 | ClientInLobbyPlayerProgression | bd212ab1 | 24300–24305 |
| 11 | ClientInLobbyFriends | 585b3816 | 27100–27118 | 27 | ClientOutLobbyPlayerProgression | c345520e | 24200–24203 |
| 12 | ClientOutLobbyAchievement | 5c2f28c8 | 28100–28101 | 28 | ClientInLobbyParty | c988e4b3 | 20700–20708 |
| 13 | ClientInLobbyMatchmaking | 5fcc2ef6 | 25600–25608 | 29 | ClientOutLobbyConnect | d7d457dd | 21800–21809 |
| 14 | ClientOutLobbyInstance | 6099bc5f | 21900–21902 | 30 | ClientInLobbyChat | dbe61f10 | 20400–20408 |
| 15 | ClientInLobbyInstance | 6e5d38b1 | 20600–20604 | 31 | ClientInLobbyFreeAssets | e79948f2 | 30500–30501 |
| 16 | ClientInLobbyHeroProgression | 70519a68 | 24900–24909 | 32 | ClientOutLobbyMatchmaking | ecb33337 | 22000–22011 |

Prometheus' own `PrometheusLobby` (CRC `0xF4456D46`) sorts after all of them and becomes channel 33 (§13).

**Other JAM functions** (Ghidra addresses): content dispatch `0x7ff650f09940`, binary field-list decode
`0x7ff650f0ce00` / one field `0x7ff650f0c7d0` / dynamic array `0x7ff650f0cc70` / fixed array `0x7ff650f0cd50`,
apply message 2 descriptors `0x7ff650f0a890`, send own negotiation message `0x7ff650f09620`, report error +
close `0x7ff650f07b40`, protocol route lookup by CRC `0x7ff650f06960`. Prometheus' JAM pool hooks:
`addJamProtocolPool` RVA `0x8a27d0`, `associatePool` RVA `0xf25ba0`.

---

## 13. Custom messages (T033)

Our own JAM protocol on the lobby link, registered by Prometheus inside the stock client (no patched
binary), so the lobby server can send messages that only our DLL understands, and the DLL can answer.
Tags: [G] Ghidra (static), [L] live read, [H] seen in game by the user, *design* = our choice.

### 13.1 How the client registers a protocol [G]

Every lobby protocol is registered by a small generated function that takes the **route config**
(`JamRouteConfig`, the talk's §8.1) of the lobby JAM service. `FUN_7ff650c46560` (the lobby system's
registration, param = lobby system) calls 16 `ClientIn*` and 16 `ClientOut*` registrars, each with
`route = *(*(lobbySys+0x10)+0x20) + 0x1a8`. Route config layout (all under the critical section at `+0x98`):

| offset | what |
|---|---|
| `+0x08` | tree CRC → **inbound entry** `{u32 crc; char* name; Receiver** receivers (+0x10); u64 count (+0x18); u64 cap}` (0x28 bytes) |
| `+0x30` | tree name → inbound entry (the talk's name-based matching; not used by the lobby) |
| `+0x58` | tree CRC → **pool** (the protocol's message factory) |
| `+0x80` / `+0x88` / `+0x90` | vector of **route entries** (0x28 bytes) / count / capacity |

A registrar does, for protocol `crc`, IDs `base..max-1`:
1. **Pool**: `FUN_7ff650f06960(route, crc)` looks it up; if missing, allocate a 0x70-byte pool (vtable
   `JamPool`), `refcount++` (`+0x8`), and store it: `*FUN_7ff6508827d0(route+0x58, &crc) = pool` (the map
   insert returns the value slot).
2. **Receiver** (inbound only): a 0x40-byte receiver `{vtable, int refcount, …, +0x18 handler object,
   +0x20 handler fn, +0x28 message queue}` (`FUN_7ff650c4f510`) appended to the inbound entry for `crc` (the
   generated code inlines the lookup/insert; `FUN_7ff650f06170(route, pool, 1 /*by CRC*/, receiver)` is the
   same thing as a function: find or create the entry, then `FUN_7ff650f05ba0` appends with `refcount++`).
3. **Route entry**: `FUN_7ff650f064a0(route, &entry, dir)` with `entry = {u32 crc; u32 base; u32 max;
   u32 flags; u32 serverType; string name (+0x18)}`. `dir` is written to `flags` (1 = the client sends it,
   `ClientOut*`; 2 = the client receives it, `ClientIn*`); registering an existing CRC again ORs `dir` in.
   `serverType` = **13** for every lobby protocol. The name string is the engine string
   `{u32 len|0x80000000 growable; u32 cap|0x80000000 inline; char inline[8] / char* ptr}`; the stock
   registrars pass an empty name.

The receiving side of a pool (vtable at `+0`, `this` first) [G, L: stub bytes of `ClientInLobbyConnect`'s pool]:

| slot | what |
|---|---|
| 0 | deleting destructor `(this, flags)` |
| 1 | destroy (called when `refcount` drops to 0) |
| 2 | `const char* name(this)`, 3 `u32 crc(this)`, 4 returns 0 |
| 5 | `u16 minId(this)` (= base), 6 `u16 maxId(this)` (exclusive) |
| 7 | `JamMessage* allocate(this, u16 id)` (new message, refcount 0; null = unknown id → link dropped) |
| 8 | `release(this, msg)` (destroy + return to the pool), 9 `clone(this, msg)` |

A **message object**: `+0x00 vtable`, `+0x08 int refcount`, `+0x10` peer JamID (16 bytes, init = the null
JamID at `DAT_7ff65188fc18`), `+0x20 u8 channel` (init 0xFF), `+0x24..+0x3F` header fields (sequence /
object id from the channel flags), `+0x40 u32 frame length`, `+0x44 u32 channel mode`, `+0x50/+0x58` an
intrusive list link, `+0x60` owning pool; **fields start at `+0x68`** (e.g. `ClientOutLobbyConnect`'s first
field is at `0x68`). Message vtable: 0 destructor `(this, flags)`, 1 destroy (at refcount 0; returns it to
`+0x60`'s pool), 2 `JamMessageInfo* info(this)`, 3 `clone(this)`. `JamMessageInfo = {name, protocol name,
JamMessageItemInfo* items, u32 protocol CRC (+0x18), u16 message id (+0x1C)}`; `items` must not be null (an
empty message has a table with only the type-17 terminator).

### 13.2 Negotiation and channels [G]

- A JamLink gets its own route config (`link+0x90`, `FUN_7ff650f08040`), a **copy** merged from the
  service's route config (`FUN_7ff650f069f0`: receivers, names, pools with `refcount++`, and route entries
  via `FUN_7ff650f064a0`). So a protocol registered on the service's route config **before the link
  connects** is on every later link.
- On connect, `FUN_7ff650f0a600` keeps the route entries whose `serverType` equals the peer's type (bits
  40–55 of the peer JamID; 13 for our lobby), **sorts them by CRC**, sends the CRC list as negotiation
  message 1 and creates channels 1..n in that order (`FUN_7ff650f0ab10`: channel `{u8 id; u8 mode = 1
  binary; u32 crc; u32 flags = the entry's flags; string name; pool = FUN_7ff650f06960(crc)}`). The server
  accepts with message 3 (§12.5).
- Channel `flags` also select header fields (`FUN_7ff650f0a090`: `0x08` u32 sequence, `0x10…0x100` an object
  id). Direction values 1/2/3 don't touch those bits, so a custom protocol registered with `dir = 3` has the
  plain `[u24 len][u8 ch][u8 index][payload]` frame.

### 13.3 Receiving and sending [G]

- **Receive** (`FUN_7ff650f091f0` → `FUN_7ff650f09940`): channel byte → channel; the inbound entry for the
  channel's CRC (`FUN_7ff650f06740`); no entry = "No route for channel" and the link is closed. Then the
  message is decoded (`FUN_7ff650f0e5d0`: index byte + `pool->minId()` → `pool->allocate(id)`; binary
  fields by the item table, §12.1), the link fills the header (`+0x10` peer JamID, `+0x20` channel, `+0x40`
  length, `+0x44` mode) and **every receiver's vtable slot 2 is called: `receive(receiver, link, msg, 0)`**,
  with `refcount` held around the call. The stock receiver (`FUN_7ff65089c940`) takes a reference and
  queues the message for its system (the talk's message-queue model) or calls the handler directly.
- **Send**: `FUN_7ff650f08af0(link, msg)` (= `JamLink::Send`): requires the link's connection in state 5,
  finds the channel by `info(msg)->crc` in `link+0xb8`, encodes `index = info->id - pool->minId()` + the
  fields (`FUN_7ff650f0e410`) and writes the frame. It doesn't take ownership of `msg`. A frame not smaller
  than `link+0x84` closes the link, so large payloads must be capped by the sender.

### 13.4 What Prometheus does (*design*; code: `prometheus/Prometheus-Core/Network/CustomJam.*`)

- Hooks `FUN_7ff650c46560` (lobby registration). After the original runs, it registers each custom
  protocol on the same route config: its own pool object (the vtable above, implemented in the DLL, with a
  permanent reference so the game never frees it), one receiver (vtable slot 2 = queue the message), and
  the route entry `{crc, base, max, dir 3, serverType 13, name}`. Idempotent: skipped if
  `FUN_7ff650f06960(route, crc)` already finds the pool.
- Messages are DLL objects with the stock base layout (§13.1); their string fields point into a buffer
  inside the message (`len` without the growable bit), so the decoder copies into it without touching the
  game heap and a too-long string is truncated, never overflowed.
- The receiver takes a reference and queues `(link, msg)`; Prometheus drains the queue **on the UI thread**
  once per frame, acts, answers through `JamLink::Send` on the link the call came from, and drops the
  reference (`destroy` at 0). A link seen closing (`JamLink::Close` hook) is forgotten, so no answer is sent
  on it.
- The mechanism is generic: a protocol is a descriptor `{name, crc, base, max, serverType, messages with
  field tables}`; the game (instance) link is a later second descriptor (its protocols travel inside the
  instance data payload stream 0 rather than as link frames, §12.3, so its receive side is a separate piece
  of work).

### 13.5 The `PrometheusLobby` protocol (*design*)

| | |
|---|---|
| name | `PrometheusLobby` |
| CRC | `0xF4456D46` = `0xF0000000 \| (crc32("PrometheusLobby") & 0x0FFFFFFF)`. Custom CRCs get the top nibble `F` so they **sort after every stock lobby protocol** (largest stock CRC `0xECB33337`): the stock channels stay 1..32 and ours is channel 33 |
| message IDs | 32000..32015 (max 32016; stock lobby IDs end at 31805) |
| server type / dir | 13 / both (3) |

| ID | index | name | direction | fields (binary, §12.1) |
|---|---|---|---|---|
| 32000 | 0 | `PrometheusCall` | server → client | `u32 seq`, `str tool`, `str args` (JSON object) |
| 32001 | 1 | `PrometheusResult` | client → server | `u32 seq`, `bool ok`, `str result` (JSON) |

- `PrometheusCall` runs a Prometheus **tool** by name with the JSON arguments: every MCP tool
  (`prometheus/Prometheus-Core/MCP/McpTools.cpp`) plus DLL-side tools that only exist for this channel
  (`notify`: an on-screen overlay message). Write-type MCP tools keep their own write-toggle check, so the
  server cannot write game memory unless the user has enabled writes in the MCP Server window.
- `PrometheusResult` answers every call with the same `seq`: `ok` false = unknown tool, bad JSON, or the
  tool returned `{"error": …}`. The result is capped at 16 KiB (`truncated: true` in a wrapper object when
  cut), the arguments at 60 KiB.
- Example frame (server → client, channel 33): `00 00 1b | 21 | 00 | 07 00 00 00 | "notify\0" |
  "{\"text\":\"hi\"}\0"` (length `0x1b` = channel + index + 25 payload bytes).

### 13.6 Server side

- **Gateway** (`server/internal/lobby`): negotiation already maps channels by CRC; a CRC missing from
  its table became `unknown-<crc>` with no message range, so nothing could be sent or received on it.
  Change: a hand-written custom protocol table (`custom_protocols.go`) next to the generated one, so
  `PrometheusLobby` gets its name and range. Backward compatible: a client without the DLL registration
  simply doesn't offer the CRC (no channel 33, sends fail with "no channel"); an old gateway with a new
  client leaves channel 33 as `unknown` and everything else unchanged. The change needs one lobby group
  restart (user's call).
- **lobbylogic**: `!custom <tool> [json]` (chat or `command("custom …")` through the gateway's `/eval`)
  sends `PrometheusCall`; `PrometheusResult` is logged and echoed as a chat line.

### 13.7 Status (2026-09-29)

- [L] With the DLL, the client offers **33** protocols; the gateway maps channel 33 = `PrometheusLobby`, and
  channels 1..32 are unchanged. MCP `custom_jam_status` shows `registered: true` and the route config.
- [L] Round trip: `!custom ping` → `PrometheusCall` (12 bytes: `01000000 70696e6700 7b7d00`) → `PrometheusResult`
  `{"pong":true,"protocol":"PrometheusLobby"}`. An MCP tool (`game_state`) returns its JSON. A write tool
  (`write_memory`) with writes off returns `ok=false`, `"writes disabled (toggle in MCP Server window)"`.
- [H] The `notify` overlay text and the `[prometheus #n ok] …` chat lines are visible on the title screen.
- Usage: `curl -X POST --data 'command("custom notify {\"text\":\"hi\",\"seconds\":10}")' http://127.0.0.1:3726/eval`,
  or type `!custom tools` in chat. From Go code: `ctx.PrometheusCall(tool, args)`; results arrive at
  `onPrometheusResult` (`server/internal/lobbylogic/custom.go`).
- Open: `PrometheusLobby` is not in `systems/data/jam/catalogue.json` (the Go `jam` package decodes it as
  `Raw`). The results are only logged: there is no request/response waiting on the server side yet.
  The game-link variant (T050's debug channel) is not done.

### 13.8 `PrometheusServerView` 32002 (T072)

The live server view (DebugDrawing.md §5): the instance server's state every tick, relayed by lobbylogic.

| ID | index | name | direction | fields (binary, §12.1) | Prometheus object offsets |
|---|---|---|---|---|---|
| 32002 | 2 | `PrometheusServerView` | server → client | `u32 seq`, `u32 frame`, `u64 sent_us`, `u64 relay_us`, `str body` (JSON, ≤ 8 KiB) | `+0x68`, `+0x6C`, `+0x70`, `+0x78`, `+0x80` |

- `seq` counts per subscription (gaps = drops), `frame` is the server's command frame, `sent_us` / `relay_us` are
  the instance's and lobbylogic's wall clocks (Unix µs) when the frame was built / forwarded. No answer.
- Types: u32 = 6, u64 = 8, str = 12. Frame on the wire: `[u24 len][u8 33][u8 index 2][u32][u32][u64][u64][body\0]`.
- A Prometheus without the message (older DLL) gets index 2 from the pool's `allocate`, which returns null for an
  unknown id: **the client drops the lobby link**. So `!serverview on` first calls the Prometheus tool
  `server_view` (`PrometheusCall`, `{"action":"hello"}`); lobbylogic subscribes the session only when that result
  comes back `ok` with `"message": 32002`. An older DLL answers "unknown tool" and nothing is streamed.
- [L, H] 2026-09-29: 62.5 frames/s over channel 33 for > 16 000 frames without a drop, 0.2–0.3 ms one-way on
  localhost, bodies up to 4.6 KB (no link close); the text shows in game. Numbers and costs: DebugDrawing.md §5.5.
