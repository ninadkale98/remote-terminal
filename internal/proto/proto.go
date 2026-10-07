// Package proto defines the JSON messages exchanged between the rterm client,
// the host-side `rterm host rpc` command, and the session agent.
package proto

import (
	"bufio"
	"encoding/json"
	"io"
)

// Version is the rterm release version. Client and host must match on Proto.
const Version = "0.1.0"

// Proto is the wire protocol version.
const Proto = 1

// DefaultSession is the session used when none is named.
const DefaultSession = "agent"

// Request is one call to the agent.
type Request struct {
	Op        string   `json:"op"`
	Session   string   `json:"session,omitempty"`
	Cmd       string   `json:"cmd,omitempty"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
	Lines     int      `json:"lines,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Cols      int      `json:"cols,omitempty"`
	Rows      int      `json:"rows,omitempty"`
	ReadOnly  bool     `json:"readonly,omitempty"`
	Token     string   `json:"token,omitempty"`
}

// SessionInfo describes one running session.
type SessionInfo struct {
	Name    string `json:"name"`
	Shell   string `json:"shell"`
	PID     int    `json:"pid"`
	Busy    bool   `json:"busy"`
	Viewers int    `json:"viewers"`
}

// Response is the agent's answer.
type Response struct {
	V          int           `json:"v"`
	OK         bool          `json:"ok"`
	Error      string        `json:"error,omitempty"`
	Status     string        `json:"status,omitempty"` // done | running | busy | matched | timeout
	ExitCode   *int          `json:"exit_code,omitempty"`
	Output     string        `json:"output,omitempty"`
	Truncated  bool          `json:"truncated,omitempty"`
	DurationMs int64         `json:"duration_ms,omitempty"`
	Version    string        `json:"version,omitempty"`
	OS         string        `json:"os,omitempty"`
	Arch       string        `json:"arch,omitempty"`
	Hostname   string        `json:"hostname,omitempty"`
	Sessions   []SessionInfo `json:"sessions,omitempty"`
}

// Err builds an error response.
func Err(msg string) Response { return Response{V: Proto, OK: false, Error: msg} }

// Write sends one JSON message followed by a newline.
func Write(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ReadLine reads one JSON message terminated by a newline.
func ReadLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return err
	}
	return json.Unmarshal(line, v)
}
