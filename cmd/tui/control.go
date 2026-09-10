package main

import (
	"fmt"
	"os"

	"github.com/oisee/open-diag-go-pro/pkg/diag"
)

// The Control Framework: after logon the server calls into the GUI over
// RFC_TR.00 items (the SAP Easy Access tree, toolbars, the frontend's
// capabilities) and waits for the GUI's RFC_TR.01 answers before it goes on.
// We cannot compute those answers, but a real GUI's, captured, can be
// replayed: the answers are the frontend's own properties and results, not
// the session's, so the same ones serve a new session when the server asks
// the same questions in the same order. Each server RFC_TR.00 is answered
// with the next captured RFC_TR.01, its counter rewritten. When the captured
// answers run out the call is left unanswered and said so.

// controlAnswers reads every C->S frame of a capture that carries an
// RFC_TR.01, in order.
func controlAnswers(path string) ([][]byte, error) {
	frames, err := clientFrames(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, fr := range frames {
		m, err := diag.ParseMessage(fr, false)
		if err != nil {
			continue
		}
		if hasRFCTR(diag.ParseItems(m.Body), 0x01) {
			out = append(out, fr)
		}
	}
	return out, nil
}

// answerControl answers one server RFC_TR.00 with the next captured
// RFC_TR.01, or reports that none is left. It returns whether one was sent.
func (s *session) answerControl(counter uint32) (bool, error) {
	if s.controlNext >= len(s.controls) {
		fmt.Fprintf(os.Stderr, "tui: RFC_TR.00 from the server, no captured answer left (%d used)\n", s.controlNext)
		return false, nil
	}
	out, err := recount(s.controls[s.controlNext], counter, s.compress)
	if err != nil {
		return false, err
	}
	s.controlNext++
	if err := s.send(out); err != nil {
		return false, err
	}
	fmt.Fprintf(os.Stderr, "tui: RFC_TR.01 answer %d/%d sent\n", s.controlNext, len(s.controls))
	return true, nil
}
