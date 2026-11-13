package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"time"
)

// Shared is a virtual display kept for the whole run, with a VNC server on
// it, so the login browsers can be watched and taken over (through noVNC)
// when a login needs a person: a CAPTCHA, a new-device check. Browsers use
// it through Options.Display.
type Shared struct {
	Display string // the X display, ":99"
	VNCAddr string // the VNC server, on the loopback interface only
	xvfb    *exec.Cmd
	vnc     *exec.Cmd
}

// StartShared starts the display and a VNC server for it on 127.0.0.1:port.
// The server has no password: only HuntDeck, on the same machine, reaches it.
func StartShared(ctx context.Context, port int) (*Shared, error) {
	path, err := exec.LookPath("x11vnc")
	if err != nil {
		return nil, errors.New("browser: no x11vnc to show the display (install it, or turn VNC off)")
	}
	xvfb, display, err := startXvfb(ctx)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	//nolint:gosec,noctx // a fixed program and arguments; it outlives ctx, like the display
	vnc := exec.Command(path, "-display", display, "-localhost", "-rfbport", strconv.Itoa(port),
		"-forever", "-shared", "-nopw", "-quiet", "-noxdamage")
	if err := vnc.Start(); err != nil {
		stopXvfb(xvfb)
		return nil, fmt.Errorf("browser: start x11vnc: %w", err)
	}
	s := &Shared{Display: display, VNCAddr: addr, xvfb: xvfb, vnc: vnc}
	// Wait until it listens, so the first viewer doesn't find it closed.
	deadline := time.Now().Add(15 * time.Second)
	for {
		d := net.Dialer{Timeout: time.Second}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			return s, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			s.Close()
			return nil, fmt.Errorf("browser: x11vnc didn't listen on %s", addr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Close stops the VNC server and the display.
func (s *Shared) Close() {
	if s.vnc != nil && s.vnc.Process != nil {
		_ = s.vnc.Process.Kill()
		_ = s.vnc.Wait()
	}
	stopXvfb(s.xvfb)
}
