package lobby

// Custom protocols: our own JAM protocols, registered in the client by Prometheus
// (docs/JAM.md §13), not by the game. The client offers their CRCs in its
// negotiation list like any other lobby protocol; this table gives them a name
// and a message range so frames on their channel are routed and sendable.
// Custom CRCs have the top nibble F, so they sort after every stock lobby
// protocol and the stock channels stay 1..32.
var customProtocols = []Protocol{
	// 32000 PrometheusCall {u32 seq, str tool, str args}   server -> client
	// 32001 PrometheusResult {u32 seq, bool ok, str result} client -> server
	{Name: "PrometheusLobby", CRC: 0xf4456d46, Base: 32000, Max: 32016, ServerType: ServerTypeLobby},
}

func init() { protocols = append(protocols, customProtocols...) }
