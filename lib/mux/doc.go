// Package mux implements a stream-oriented multiplexer over net.Conn.
//
// The wire format is kept compatible with older nps mux peers. Newer changes in
// this package focus on transport adaptation, timeout behavior, and runtime
// robustness without changing protocol compatibility.
package mux
