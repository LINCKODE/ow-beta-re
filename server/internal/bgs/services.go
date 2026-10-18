package bgs

import (
	"hash/fnv"

	"google.golang.org/protobuf/reflect/protoreflect"

	authenticationv1 "owserver/gen/bgs/protocol/authentication/v1"
	challengev1 "owserver/gen/bgs/protocol/challenge/v1"
	connectionv1 "owserver/gen/bgs/protocol/connection/v1"
	game_utilitiesv1 "owserver/gen/bgs/protocol/game_utilities/v1"
)

// Service is one bgs service.
//
// On the wire the 0.8 client identifies services by the FNV-1a hash of their
// original "bnet.protocol.*" name (Header.service_hash), not of the
// "bgs.protocol.*" name in the protos. Both hashes appear in the binary.
type Service struct {
	Desc     protoreflect.ServiceDescriptor
	WireName string                                   // original name, e.g. "bnet.protocol.connection.ConnectionService"
	WireHash uint32                                   // Hash(WireName), what the client sends
	Methods  map[uint32]protoreflect.MethodDescriptor // keyed by method ID
	// Exported is true for services the server implements (the client calls them);
	// false for listeners the client implements (the server calls them).
	Exported bool
}

// methodIDs are the wire method IDs, read from the 0.8 client's registration
// tables (each entry is registered as (proto-name hash << 32) | id). They do not
// follow declaration order: e.g. AuthenticationListener starts at 4, and methods
// the client does not register are left out.
var methodIDs = map[protoreflect.FullName]map[protoreflect.Name]uint32{
	"bgs.protocol.connection.v1.ConnectionService": {
		"Connect": 1, "Echo": 3, "ForceDisconnect": 4, "KeepAlive": 5, "RequestDisconnect": 7,
	},
	"bgs.protocol.authentication.v1.AuthenticationService": {
		"Logon": 1, "GenerateSSOToken": 5, "VerifyWebCredentials": 7, "GenerateWebCredentials": 8,
	},
	"bgs.protocol.authentication.v1.AuthenticationListener": {
		"OnServerStateChange": 4, "OnLogonComplete": 5, "OnMemModuleLoad": 6, "OnLogonUpdate": 10,
		"OnVersionInfoUpdated": 11, "OnLogonQueueUpdate": 12, "OnLogonQueueEnd": 13,
	},
	"bgs.protocol.challenge.v1.ChallengeService": {
		"ChallengePicked": 1, "ChallengeAnswered": 2, "ChallengeCancelled": 3, "SendChallengeToUser": 4,
	},
	"bgs.protocol.challenge.v1.ChallengeListener": {
		"OnChallengeUser": 1, "OnChallengeResult": 2, "OnExternalChallenge": 3, "OnExternalChallengeResult": 4,
	},
	"bgs.protocol.game_utilities.v1.GameUtilitiesService": {
		"ProcessClientRequest": 1, "PresenceChannelCreated": 2, "GetPlayerVariables": 3, "ProcessServerRequest": 6,
		"OnGameAccountOnline": 7, "OnGameAccountOffline": 8, "GetAchievementsFile": 9, "GetAllValuesForAttribute": 10,
	},
}

// MethodID returns the wire ID of a method, or false if the client doesn't register it.
func MethodID(m protoreflect.MethodDescriptor) (uint32, bool) {
	id, ok := methodIDs[m.Parent().FullName()][m.Name()]
	return id, ok
}

// Hash returns the FNV-1a 32-bit hash of a service name.
func Hash(name string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(name))
	return h.Sum32()
}

// The six services the 0.8 client registers - no more, no less.
var serviceList = func() []*Service {
	defs := []struct {
		desc     protoreflect.ServiceDescriptor
		wireName string
		exported bool
	}{
		{connectionv1.File_bgs_protocol_connection_v1_connection_service_proto.Services().ByName("ConnectionService"), "bnet.protocol.connection.ConnectionService", true},
		{authenticationv1.File_bgs_protocol_authentication_v1_authentication_service_proto.Services().ByName("AuthenticationService"), "bnet.protocol.authentication.AuthenticationServer", true},
		{challengev1.File_bgs_protocol_challenge_v1_challenge_service_proto.Services().ByName("ChallengeService"), "bnet.protocol.challenge.ChallengeService", true},
		{game_utilitiesv1.File_bgs_protocol_game_utilities_v1_game_utilities_service_proto.Services().ByName("GameUtilitiesService"), "bnet.protocol.game_utilities.GameUtilities", true},
		{authenticationv1.File_bgs_protocol_authentication_v1_authentication_service_proto.Services().ByName("AuthenticationListener"), "bnet.protocol.authentication.AuthenticationClient", false},
		{challengev1.File_bgs_protocol_challenge_v1_challenge_service_proto.Services().ByName("ChallengeListener"), "bnet.protocol.challenge.ChallengeNotify", false},
	}
	services := make([]*Service, len(defs))
	for i, d := range defs {
		s := &Service{Desc: d.desc, WireName: d.wireName, WireHash: Hash(d.wireName), Methods: map[uint32]protoreflect.MethodDescriptor{}, Exported: d.exported}
		methods := d.desc.Methods()
		for j := 0; j < methods.Len(); j++ {
			if id, ok := MethodID(methods.Get(j)); ok {
				s.Methods[id] = methods.Get(j)
			}
		}
		services[i] = s
	}
	return services
}()

// Services maps both the wire hash and the hash of the proto name to each service.
var Services = func() map[uint32]*Service {
	m := make(map[uint32]*Service, 2*len(serviceList))
	for _, s := range serviceList {
		m[s.WireHash] = s
		m[Hash(string(s.Desc.FullName()))] = s
	}
	return m
}()

// ServiceByName looks up a service by its full proto name.
func ServiceByName(fullName protoreflect.FullName) *Service {
	return Services[Hash(string(fullName))]
}
