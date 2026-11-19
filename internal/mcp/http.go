package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
)

// HTTPVersions are the MCP revisions ServeHTTP speaks, the newest first. What
// it implements of them is the same in each: tools, answered as JSON.
var HTTPVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", ProtocolVersion}

// maxHTTPBody bounds a request: a tool call's arguments, not a file.
const maxHTTPBody = 4 << 20

// ServeHTTP serves the server's tools over MCP's Streamable HTTP transport,
// for a client somewhere else: each POST carries a JSON-RPC message (or a
// batch of them) and is answered with JSON, never a stream. There is no
// session: nothing is kept between requests, so a server may be made for each
// one. Who the caller is and what it may do is the caller's wrapper's
// business — nothing here authenticates anybody.
//
// A waiting tool runs for as long as the request lasts and ends when the
// client hangs up.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// GET opens a stream for what the server has to say unasked, which
		// is nothing, and DELETE ends a session, of which there are none.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "this MCP server answers POST requests only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHTTPBody))
	if err != nil {
		writeRPC(w, http.StatusRequestEntityTooLarge, message{JSONRPC: "2.0", Error: &rpcError{Code: codeInvalidRequest, Message: "the request is too large"}})
		return
	}
	var msgs []message
	batch := len(bytes.TrimSpace(body)) > 0 && bytes.TrimSpace(body)[0] == '['
	if batch {
		err = json.Unmarshal(body, &msgs)
	} else {
		var msg message
		err = json.Unmarshal(body, &msg)
		msgs = []message{msg}
	}
	if err != nil {
		writeRPC(w, http.StatusBadRequest, message{JSONRPC: "2.0", Error: &rpcError{Code: codeParse, Message: "invalid JSON"}})
		return
	}
	var out []message
	for _, msg := range msgs {
		// Notifications, and answers to requests this server never makes,
		// take no answer.
		if len(msg.ID) == 0 || msg.Method == "" {
			continue
		}
		reply := message{JSONRPC: "2.0", ID: msg.ID}
		if t, args, ok := s.waitingTool(msg); ok {
			text, err := t.Wait(r.Context(), args)
			if err != nil {
				reply.Result = result([]Content{Text(err.Error())}, true)
			} else {
				reply.Result = result([]Content{Text(text)}, false)
			}
		} else {
			reply.Result, reply.Error = s.handle(msg)
			if res, ok := reply.Result.(map[string]any); ok && msg.Method == "initialize" {
				res["protocolVersion"] = negotiate(msg.Params)
			}
		}
		out = append(out, reply)
	}
	switch {
	case len(out) == 0:
		w.WriteHeader(http.StatusAccepted)
	case batch:
		writeRPC(w, http.StatusOK, out)
	default:
		writeRPC(w, http.StatusOK, out[0])
	}
}

// negotiate answers the revision the client asked for when this server speaks
// it, and its newest otherwise, which the client then takes or leaves.
func negotiate(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && slices.Contains(HTTPVersions, p.ProtocolVersion) {
		return p.ProtocolVersion
	}
	return HTTPVersions[0]
}

func writeRPC(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
