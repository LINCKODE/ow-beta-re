#pragma once
#include <cstdint>
#include <string>

// Development bridge to our game server's control API (server/internal/instance
// control.go, 127.0.0.1:3728): makes a Statescript change the client made locally
// authoritative, so it is not rolled back.
//
// The client only predicts what a node does: the node runs on the next Statescript
// tick and is rolled back shortly after when the server doesn't confirm it. A capture
// taken a couple of frames after the change (ss_serialize_local: every instance,
// active state and variable of the entity) and sent back by the server as a full
// update makes it stick.
namespace ServerBridge {

// Capture the game EA entity's Statescript state `delay_frames` UI frames from now
// and send it to the game server as a full update.
void queue_capture(uint32_t entity_id, int delay_frames = 2);

// Echo mode: keep confirming the entity's Statescript state (a full update, then
// every few frames a delta of what changed, stamped with the client's own frame), so
// everything the client predicts for it sticks. A dev stand-in for a server that
// simulates the scripts itself.
void set_echo(uint32_t entity_id, bool enabled);
bool echo_enabled();
uint32_t echo_entity();

// Runs queued captures and echo mode; call once per UI frame on the UI thread.
void process();

// The outcome of the last capture, for display.
std::string last_status();

}
