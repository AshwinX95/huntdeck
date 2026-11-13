package browser

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// startXvfb starts a virtual display and returns it and its name (":99").
// Xvfb picks a free display number and writes it to the given descriptor.
func startXvfb(ctx context.Context) (*exec.Cmd, string, error) {
	path, err := exec.LookPath("Xvfb")
	if err != nil {
		return nil, "", errors.New("browser: no display, and no Xvfb to make one (install it, or set Headless)")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = r.Close() }()
	// fd 3 in Xvfb is w.
	cmd := exec.Command(path, "-displayfd", "3", "-screen", "0", "1920x1080x24", "-nolisten", "tcp") //nolint:gosec,noctx // outlives ctx, like Chrome
	cmd.ExtraFiles = []*os.File{w}
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return nil, "", fmt.Errorf("browser: start Xvfb: %w", err)
	}
	_ = w.Close()
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		got <- strings.TrimSpace(line)
	}()
	select {
	case n := <-got:
		if n == "" {
			stopXvfb(cmd)
			return nil, "", errors.New("browser: Xvfb didn't start")
		}
		return cmd, ":" + n, nil
	case <-ctx.Done():
		stopXvfb(cmd)
		return nil, "", fmt.Errorf("browser: Xvfb: %w", ctx.Err())
	case <-time.After(15 * time.Second):
		stopXvfb(cmd)
		return nil, "", errors.New("browser: Xvfb didn't start in time")
	}
}

// stopXvfb ends a display startXvfb started, if any.
func stopXvfb(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
