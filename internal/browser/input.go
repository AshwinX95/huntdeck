package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// Input is sent as a person's would be: keys with their key codes and Shift
// where a US keyboard needs it, and a mouse that moves to what it clicks.
// Pages that score visitors (reCAPTCHA) read these events.

// key is how a character is typed on a US keyboard.
type key struct {
	code    string // the physical key, as KeyboardEvent.code
	keyCode int    // the Windows virtual key code
	shift   bool
}

// usShifted and usUnshifted map the symbol keys, shifted and not, to their
// codes and key codes.
var (
	usUnshifted = map[rune]key{
		'-': {"Minus", 189, false}, '=': {"Equal", 187, false}, '[': {"BracketLeft", 219, false},
		']': {"BracketRight", 221, false}, '\\': {"Backslash", 220, false}, ';': {"Semicolon", 186, false},
		'\'': {"Quote", 222, false}, ',': {"Comma", 188, false}, '.': {"Period", 190, false},
		'/': {"Slash", 191, false}, '`': {"Backquote", 192, false}, ' ': {"Space", 32, false},
	}
	usShifted = map[rune]key{
		'!': {"Digit1", 49, true}, '@': {"Digit2", 50, true}, '#': {"Digit3", 51, true}, '$': {"Digit4", 52, true},
		'%': {"Digit5", 53, true}, '^': {"Digit6", 54, true}, '&': {"Digit7", 55, true}, '*': {"Digit8", 56, true},
		'(': {"Digit9", 57, true}, ')': {"Digit0", 48, true}, '_': {"Minus", 189, true}, '+': {"Equal", 187, true},
		'{': {"BracketLeft", 219, true}, '}': {"BracketRight", 221, true}, '|': {"Backslash", 220, true},
		':': {"Semicolon", 186, true}, '"': {"Quote", 222, true}, '<': {"Comma", 188, true},
		'>': {"Period", 190, true}, '?': {"Slash", 191, true}, '~': {"Backquote", 192, true},
	}
)

// keyFor returns how r is typed, and false for a character a US keyboard
// has no key for (it is then sent as text alone).
func keyFor(r rune) (key, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return key{"Key" + strings.ToUpper(string(r)), int(r - 'a' + 'A'), false}, true
	case r >= 'A' && r <= 'Z':
		return key{"Key" + string(r), int(r), true}, true
	case r >= '0' && r <= '9':
		return key{"Digit" + string(r), int(r), false}, true
	}
	if k, ok := usUnshifted[r]; ok {
		return k, true
	}
	k, ok := usShifted[r]
	return k, ok
}

const shiftModifier = 8

// Type types text into the focused element a key at a time, about perKey
// apart (varied, as a person's typing is).
func (p *Page) Type(ctx context.Context, text string, perKey time.Duration) error {
	shifted := false
	for _, r := range text {
		k, known := keyFor(r)
		if k.shift != shifted {
			typ := "keyUp"
			if k.shift {
				typ = "rawKeyDown"
			}
			if err := p.key(ctx, map[string]any{"type": typ, "key": "Shift", "code": "ShiftLeft", "windowsVirtualKeyCode": 16,
				"modifiers": map[bool]int{true: shiftModifier}[k.shift]}); err != nil {
				return err
			}
			shifted = k.shift
		}
		ch := string(r)
		down := map[string]any{"type": "keyDown", "key": ch, "text": ch, "unmodifiedText": ch}
		up := map[string]any{"type": "keyUp", "key": ch}
		if known {
			for _, m := range []map[string]any{down, up} {
				m["code"], m["windowsVirtualKeyCode"], m["nativeVirtualKeyCode"] = k.code, k.keyCode, k.keyCode
				if k.shift {
					m["modifiers"] = shiftModifier
				}
			}
		}
		if err := p.key(ctx, down); err != nil {
			return err
		}
		if err := sleep(ctx, jitter(perKey/3)); err != nil {
			return err
		}
		if err := p.key(ctx, up); err != nil {
			return err
		}
		if err := sleep(ctx, jitter(perKey)); err != nil {
			return err
		}
	}
	if shifted {
		return p.key(ctx, map[string]any{"type": "keyUp", "key": "Shift", "code": "ShiftLeft", "windowsVirtualKeyCode": 16})
	}
	return nil
}

// Press presses Enter.
func (p *Page) Press(ctx context.Context) error {
	for _, typ := range []string{"keyDown", "keyUp"} {
		params := map[string]any{"type": typ, "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13, "nativeVirtualKeyCode": 13}
		if typ == "keyDown" {
			params["text"] = "\r"
		}
		if err := p.key(ctx, params); err != nil {
			return err
		}
	}
	return nil
}

func (p *Page) key(ctx context.Context, params map[string]any) error {
	_, err := p.b.call(ctx, p.session, "Input.dispatchKeyEvent", params)
	return err
}

// Click moves the mouse to a point inside the element matching a CSS
// selector, over a few steps as a hand would, and clicks it.
func (p *Page) Click(ctx context.Context, selector string) error {
	sel, _ := json.Marshal(selector)
	var box []float64
	if err := p.Eval(ctx, `(() => { const e = document.querySelector(`+string(sel)+`); if (!e) return null;
		e.scrollIntoView({block: 'center'}); const r = e.getBoundingClientRect(); return [r.left, r.top, r.width, r.height] })()`, &box); err != nil {
		return err
	}
	if len(box) != 4 || box[2] < 1 || box[3] < 1 {
		return fmt.Errorf("browser: no visible element matches %s", selector)
	}
	// Somewhere in the middle of it, not its exact centre.
	x := box[0] + box[2]*(0.3+0.4*rand.Float64())  //nolint:gosec // not a secret
	y := box[1] + box[3]*(0.35+0.3*rand.Float64()) //nolint:gosec // not a secret
	steps := 12 + rand.IntN(10)                    //nolint:gosec // not a secret
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		t = t * t * (3 - 2*t) // ease in and out
		if err := p.mouse(ctx, "mouseMoved", p.x+(x-p.x)*t, p.y+(y-p.y)*t); err != nil {
			return err
		}
		if err := sleep(ctx, jitter(15*time.Millisecond)); err != nil {
			return err
		}
	}
	p.x, p.y = x, y
	if err := sleep(ctx, jitter(120*time.Millisecond)); err != nil {
		return err
	}
	if err := p.mouse(ctx, "mousePressed", x, y); err != nil {
		return err
	}
	if err := sleep(ctx, jitter(80*time.Millisecond)); err != nil {
		return err
	}
	return p.mouse(ctx, "mouseReleased", x, y)
}

func (p *Page) mouse(ctx context.Context, typ string, x, y float64) error {
	params := map[string]any{"type": typ, "x": x, "y": y}
	if typ != "mouseMoved" {
		params["button"], params["clickCount"] = "left", 1
	}
	_, err := p.b.call(ctx, p.session, "Input.dispatchMouseEvent", params)
	return err
}

// jitter returns d give or take half.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + rand.N(d) //nolint:gosec // not a secret
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
