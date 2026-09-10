package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/oisee/open-diag-go-pro/pkg/alv"
	"github.com/oisee/open-diag-go-pro/pkg/diag"
)

// fromMCP reads the SAP_* environment of one server in a .mcp.json (the file
// Claude Code keeps its MCP servers in) and returns its credentials and the
// host of its SAP_URL. The password stays inside the returned struct: this
// function does not log or print it, and the address is derived only to save
// the user retyping it. A server configured for SSO (no SAP_PASSWORD) is
// refused, because plain DIAG has no SSO logon.
func fromMCP(path, server string) (credentials, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, "", err
	}
	var doc struct {
		Servers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return credentials{}, "", fmt.Errorf("%s: %w", path, err)
	}
	s, ok := doc.Servers[server]
	if !ok {
		var names []string
		for n := range doc.Servers {
			names = append(names, n)
		}
		return credentials{}, "", fmt.Errorf("%s: no server %q (have %s)", path, server, strings.Join(names, ", "))
	}
	c := credentials{
		Client:   s.Env["SAP_CLIENT"],
		User:     s.Env["SAP_USER"],
		Password: s.Env["SAP_PASSWORD"],
		Lang:     s.Env["SAP_LANGUAGE"],
	}
	if c.Password == "" {
		return c, "", fmt.Errorf("server %q has no SAP_PASSWORD; an SSO system cannot be logged on to over plain DIAG", server)
	}
	host := ""
	if u, err := url.Parse(s.Env["SAP_URL"]); err == nil {
		host = u.Hostname()
	}
	return c, host, nil
}

// Live logon. The GUI answers the logon screen with one PAI frame: the
// session id the server gave it, the fields it changed (client, user,
// password, language as input atoms at the cells the server placed them),
// its window metrics, and a running frame counter. This file builds that
// frame from a captured one — everything but the session id, the counter
// and the field atoms is copied from the template, so the frame has exactly
// the shape a real GUI's had — and the caller sends it once. One attempt per
// run: a second wrong password would count towards the user's lock, so a
// logon screen that comes back is shown, not answered.
//
// Credentials come from the command line and the ODGP_PASSWORD environment
// variable the user sets in their own shell; this program reads no
// configuration files for them and never prints the password.

// credentials is what the logon screen asks for.
type credentials struct {
	Client, User, Password, Lang string
}

// logonFields are the ABAP names of the logon screen's fields and the
// credential each takes.
var logonFields = []struct{ name, key string }{
	{"RSYST-MANDT", "client"},
	{"RSYST-BNAME", "user"},
	{"RSYST-BCODE", "password"},
	{"RSYST-LANGU", "lang"},
}

// isLogonScreen reports whether a frame's items carry the logon dynpro:
// a DYNT_ATOM naming RSYST-BNAME.
func isLogonScreen(items []diag.Item) bool {
	for _, it := range items {
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			_, byName := diag.FieldIndex(it.Value)
			if _, ok := byName["RSYST-BNAME"]; ok {
				return true
			}
		}
	}
	return false
}

// buildLogonPAI makes the client frame that answers the logon screen in
// screenItems, shaped like template (a captured client logon frame):
// the SES item takes the server's session id, ST_USER.26 the frame counter,
// and the DYNT_ATOM holds our field atoms — copies of the server's own
// input atoms with the values set and the "changed" flag on, no name atoms,
// as the GUI sends them. compress selects the LZH body the GUI uses.
func buildLogonPAI(template []byte, screenItems []diag.Item, c credentials, counter uint32, compress bool) ([]byte, error) {
	tm, err := diag.ParseMessage(template, false)
	if err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	items := diag.ParseItems(tm.Body)

	// The server's session id and its field atoms.
	var ses []byte
	var atoms []diag.Atom
	var byName map[string]int
	for _, it := range screenItems {
		if it.Type == diag.ItemSES {
			ses = it.Value
		}
		if it.Type == diag.ItemAPPL4 && it.ID == 0x09 && it.SID == 0x02 {
			a, n := diag.FieldIndex(it.Value)
			if _, ok := n["RSYST-BNAME"]; ok {
				atoms, byName = a, n
			}
		}
	}
	if atoms == nil {
		return nil, fmt.Errorf("screen has no logon fields")
	}
	values := map[string]string{"client": c.Client, "user": c.User, "password": c.Password, "lang": c.Lang}
	var out []diag.Atom
	for _, f := range logonFields {
		i, ok := byName[f.name]
		if !ok {
			continue
		}
		v := values[f.key]
		if v == "" {
			continue // leave the server's default (client and language are prefilled)
		}
		a := atoms[i]
		a.Text = v
		a.Length = len(v)
		a.Flags[1] |= 0x01 // changed by the user
		a.Rest = nil
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nothing to fill in")
	}

	hadAtom, hadSES := false, false
	for i := range items {
		it := &items[i]
		switch {
		case it.ID == 0x09 && it.SID == 0x02 && (it.Type == diag.ItemAPPL || it.Type == diag.ItemAPPL4):
			it.Value = diag.EncodeDyntAtoms(out)
			hadAtom = true
		case it.Type == diag.ItemSES:
			if len(ses) == len(it.Value) {
				it.Value = append([]byte{}, ses...)
			}
			hadSES = true
		case it.Type == diag.ItemAPPL && it.ID == 0x04 && it.SID == 0x26 && len(it.Value) == 4:
			it.Value = append([]byte{}, it.Value...)
			binary.BigEndian.PutUint32(it.Value, counter)
		}
	}
	if !hadAtom || !hadSES {
		return nil, fmt.Errorf("template is not a logon PAI (atom %v, session %v)", hadAtom, hadSES)
	}
	return encodeClient(tm.Header, items, compress)
}

// encodeClient lays a client message: the DIAG header and the items, the
// body LZH-compressed when asked (compress=1 in the header, the SAP-LZH
// stream sapcompress reads back), else plain (compress=0).
func encodeClient(h diag.Header, items []diag.Item, compress bool) ([]byte, error) {
	body := diag.EncodeItems(items)
	if !compress {
		h.Compress = 0
		return append(h.Bytes(), body...), nil
	}
	z, err := alv.Compress(body)
	if err != nil {
		return nil, err
	}
	h.Compress = 1
	return append(h.Bytes(), z...), nil
}

// counterOf reads the client frame counter (ST_USER.26) a frame carries, or 0
// when absent. The GUI increments it on each PAI; our logon answer follows the
// server's last value plus one.
func counterOf(items []diag.Item) uint32 {
	for _, it := range items {
		if it.Type == diag.ItemAPPL && it.ID == 0x04 && it.SID == 0x26 && len(it.Value) == 4 {
			return binary.BigEndian.Uint32(it.Value)
		}
	}
	return 0
}

// hasRFCTR reports whether a frame's items carry an RFC_TR item of the given
// sid — .00 is the server's Control Framework call, .01 the client's answer.
func hasRFCTR(items []diag.Item, sid byte) bool {
	for _, it := range items {
		if (it.Type == diag.ItemAPPL || it.Type == diag.ItemAPPL4) && it.ID == 0x08 && it.SID == sid {
			return true
		}
	}
	return false
}

// recount rewrites a captured client frame's counter and returns it plain or
// compressed, so a captured Control Framework answer can be replayed in
// sequence on a new session.
func recount(frame []byte, counter uint32, compress bool) ([]byte, error) {
	m, err := diag.ParseMessage(frame, false)
	if err != nil {
		return nil, err
	}
	items := diag.ParseItems(m.Body)
	for i := range items {
		it := &items[i]
		if it.Type == diag.ItemAPPL && it.ID == 0x04 && it.SID == 0x26 && len(it.Value) == 4 {
			it.Value = append([]byte{}, it.Value...)
			binary.BigEndian.PutUint32(it.Value, counter)
		}
	}
	return encodeClient(m.Header, items, compress)
}
