// Package ecs wraps the C++ ECS reimplementation (systems/ecs, C API capi/ecs.h) for Go.
//
// Build the static C API first: cmake -S systems/ecs -B systems/ecs/build && cmake --build systems/ecs/build
package ecs

/*
#cgo CFLAGS: -I${SRCDIR}/../../capi
#cgo LDFLAGS: ${SRCDIR}/../../build/libow_ecs_c_static.a ${SRCDIR}/../../build/libow_ecs.a -lstdc++ -lm
#include <stdlib.h>
#include "ecs.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Admin is one entity admin built from the live-client manifest ("game", "session", "lobby").
type Admin struct{ h *C.ecs_admin }

func NewAdmin(name string) (*Admin, error) {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	h := C.ecs_admin_create(cs)
	if h == nil {
		return nil, errors.New("ecs: unknown admin " + name)
	}
	return &Admin{h}, nil
}

func (a *Admin) Close() { C.ecs_admin_destroy(a.h) }

// Spawn creates an entity (id 0 = allocate). Returns its id, 0 on failure.
func (a *Admin) Spawn(id uint32, comps []byte, idHighBits uint32) uint32 {
	var p *C.uint8_t
	if len(comps) > 0 {
		p = (*C.uint8_t)(unsafe.Pointer(&comps[0]))
	}
	return uint32(C.ecs_spawn(a.h, C.uint32_t(id), p, C.int(len(comps)), C.uint32_t(idHighBits)))
}

func (a *Admin) RequestDelete(id uint32, immediate bool) bool {
	imm := 0
	if immediate {
		imm = 1
	}
	return C.ecs_request_delete(a.h, C.uint32_t(id), C.int(imm)) != 0
}

// Tick runs one EntityAdmin tick (the client ticks once per render frame).
func (a *Admin) Tick(dt float32) { C.ecs_tick(a.h, C.float(dt)) }

// ClockResync sets the command frame and the µs accumulator (the client resyncs to the
// server's frame plus its lead).
func (a *Admin) ClockResync(cf uint32, accumulatorMicros int64) {
	C.ecs_clock_seed(a.h, C.uint32_t(cf), C.int64_t(accumulatorMicros))
}

// SetClockDilation sets the clock's dilation flags (1 slow down, 2 speed up, 8 strong).
func (a *Admin) SetClockDilation(flags uint8) { C.ecs_clock_set_dilation(a.h, C.uint8_t(flags)) }

// CommandFrame is the last command frame the clock produced.
func (a *Admin) CommandFrame() uint32 { return uint32(C.ecs_clock_command_frame(a.h)) }

// CommandFrameListenerCount is the number of registered command-frame listeners.
func (a *Admin) CommandFrameListenerCount() int { return int(C.ecs_command_frame_listener_count(a.h)) }

func (a *Admin) TickCount() uint64 { return uint64(C.ecs_tick_count(a.h)) }
func (a *Admin) EntityCount() int  { return int(C.ecs_entity_count(a.h)) }

// Components returns the entity's component ids (sorted), nil if it doesn't exist.
func (a *Admin) Components(id uint32) []byte {
	var buf [192]C.uint8_t
	n := int(C.ecs_entity_components(a.h, C.uint32_t(id), &buf[0], C.int(len(buf))))
	if n < 0 {
		return nil
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(buf[i])
	}
	return out
}

// Event is one trace event (same kinds as the Prometheus ecs_trace tool).
type Event struct {
	Seq, Tick uint64
	Phase     string
	PhaseSys  int
	Kind      string
	Sys       int
	Ent       uint32
	A, B      uint64
	Comps     []byte
}

// TakeEvents drains the recorded trace events.
func (a *Admin) TakeEvents() []Event {
	var out []Event
	var buf [256]C.ecs_event
	for {
		n := int(C.ecs_trace_take(a.h, &buf[0], C.int(len(buf))))
		for _, e := range buf[:n] {
			comps := make([]byte, e.comp_count)
			for i := range comps {
				comps[i] = byte(e.comps[i])
			}
			out = append(out, Event{
				Seq: uint64(e.seq), Tick: uint64(e.tick),
				Phase: C.GoString(C.ecs_trace_phase_name(e.phase)), PhaseSys: int(e.phase_sys),
				Kind: C.GoString(C.ecs_trace_kind_name(e.kind)), Sys: int(e.sys),
				Ent: uint32(e.ent), A: uint64(e.a), B: uint64(e.b), Comps: comps,
			})
		}
		if n < len(buf) {
			return out
		}
	}
}
