package main

import (
	"context"
	"os"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

// The keyboard, for the interactive mode. The terminal is put in raw mode so
// every key arrives as it is pressed (no line buffering, no echo, no signal
// keys — Ctrl-C reaches us as a byte and we quit on it ourselves), and the
// bytes are decoded into keys: printable runes, editing and navigation keys,
// and the function keys. Nothing here touches the socket.

type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyTab
	keyBackTab
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyPgUp
	keyPgDn
	keyEsc
	keyCtrlC
	keyCtrlO
	keyFunc // F1..F12, number in n
)

type key struct {
	kind keyKind
	r    rune
	n    int
}

// rawTerm remembers the terminal settings raw mode replaced, to put back.
type rawTerm struct {
	fd    int
	saved syscall.Termios
	ok    bool
}

// enterRaw switches the terminal on fd to raw mode: no canonical line
// editing, no echo, no ISIG/IXON so every control key comes through.
func enterRaw(fd int) (*rawTerm, error) {
	var t syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	saved := t
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	return &rawTerm{fd: fd, saved: saved, ok: true}, nil
}

// restore puts the saved settings back. Safe to call more than once.
func (r *rawTerm) restore() {
	if r == nil || !r.ok {
		return
	}
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(r.fd), syscall.TCSETS, uintptr(unsafe.Pointer(&r.saved)))
	r.ok = false
}

// readKeys reads standard input until the context ends or input closes,
// sending each decoded key on ch. Escape sequences arrive in one read from a
// real terminal, so a read is decoded as a whole; a lone ESC is keyEsc.
func readKeys(ctx context.Context, ch chan<- key) {
	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		for _, k := range decodeKeys(buf[:n]) {
			select {
			case ch <- k:
			case <-ctx.Done():
				return
			}
		}
	}
}

// decodeKeys turns one read's bytes into keys.
func decodeKeys(b []byte) []key {
	var out []key
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			k, n := decodeEscape(b)
			out = append(out, k)
			b = b[n:]
			continue
		case c == '\r' || c == '\n':
			out = append(out, key{kind: keyEnter})
		case c == '\t':
			out = append(out, key{kind: keyTab})
		case c == 0x7f || c == 0x08:
			out = append(out, key{kind: keyBackspace})
		case c == 0x03:
			out = append(out, key{kind: keyCtrlC})
		case c == 0x0f:
			out = append(out, key{kind: keyCtrlO})
		case c < 0x20:
			// other control keys: ignored
		default:
			r, size := utf8.DecodeRune(b)
			if r == utf8.RuneError && size <= 1 {
				b = b[1:]
				continue
			}
			out = append(out, key{kind: keyRune, r: r})
			b = b[size:]
			continue
		}
		b = b[1:]
	}
	return out
}

// decodeEscape reads one escape sequence at the start of b (b[0] == ESC) and
// returns the key and how many bytes it used. An unknown sequence is skipped
// as keyEsc; a lone ESC is keyEsc.
func decodeEscape(b []byte) (key, int) {
	if len(b) == 1 {
		return key{kind: keyEsc}, 1
	}
	// SS3: ESC O x — F1..F4, and Home/End on some terminals.
	if b[1] == 'O' && len(b) >= 3 {
		switch b[2] {
		case 'P':
			return key{kind: keyFunc, n: 1}, 3
		case 'Q':
			return key{kind: keyFunc, n: 2}, 3
		case 'R':
			return key{kind: keyFunc, n: 3}, 3
		case 'S':
			return key{kind: keyFunc, n: 4}, 3
		case 'H':
			return key{kind: keyHome}, 3
		case 'F':
			return key{kind: keyEnd}, 3
		}
		return key{kind: keyEsc}, 3
	}
	if b[1] != '[' {
		return key{kind: keyEsc}, 1
	}
	// CSI: ESC [ params final
	i := 2
	num := 0
	for i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] == ';') {
		if b[i] == ';' {
			// a modifier follows; we ignore modifiers but keep the first number
			for i < len(b) && b[i] != '~' && !(b[i] >= 'A' && b[i] <= 'Z') {
				i++
			}
			break
		}
		num = num*10 + int(b[i]-'0')
		i++
	}
	if i >= len(b) {
		return key{kind: keyEsc}, len(b)
	}
	final := b[i]
	n := i + 1
	switch final {
	case 'A':
		return key{kind: keyUp}, n
	case 'B':
		return key{kind: keyDown}, n
	case 'C':
		return key{kind: keyRight}, n
	case 'D':
		return key{kind: keyLeft}, n
	case 'H':
		return key{kind: keyHome}, n
	case 'F':
		return key{kind: keyEnd}, n
	case 'Z':
		return key{kind: keyBackTab}, n
	case '~':
		switch num {
		case 1, 7:
			return key{kind: keyHome}, n
		case 4, 8:
			return key{kind: keyEnd}, n
		case 3:
			return key{kind: keyDelete}, n
		case 5:
			return key{kind: keyPgUp}, n
		case 6:
			return key{kind: keyPgDn}, n
		case 11, 12, 13, 14:
			return key{kind: keyFunc, n: num - 10}, n
		case 15:
			return key{kind: keyFunc, n: 5}, n
		case 17, 18, 19, 20, 21:
			return key{kind: keyFunc, n: num - 11}, n
		case 23, 24:
			return key{kind: keyFunc, n: num - 12}, n
		}
	}
	return key{kind: keyEsc}, n
}
