package ui

import (
	"context"
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/ens"

	"github.com/ethereum/go-ethereum/common"
)

// resolveDebounce is how long the field waits after the last keystroke before
// validating/resolving, so ENS lookups don't fire on every character.
const resolveDebounce = 350 * time.Millisecond

var (
	colorOK      = color.NRGBA{R: 0x2e, G: 0x7d, B: 0x32, A: 0xff} // green
	colorError   = color.NRGBA{R: 0xc6, G: 0x28, B: 0x28, A: 0xff} // red
	colorNeutral = color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff} // gray
)

// addressField is an ENS-aware address input. It accepts either a hex address
// (EIP-55 checksum validated) or an ENS name (forward-resolved against the active
// connection), reports resolution status inline with color, and exposes the
// resolved address. Resolution is debounced and runs off the UI thread.
type addressField struct {
	entry  *widget.Entry
	status *canvas.Text

	resolverFn func() *ens.Resolver // supplies the current resolver (or nil)
	onChange   func()               // called after validity/value changes

	mu       sync.Mutex
	resolved common.Address
	valid    bool
	gen      int // debounce/resolution generation guard
}

// newAddressField builds the widget. resolverFn is called each validation to get
// the current ENS resolver (nil when not connected); onChange (optional) fires
// after each validity change so a parent can enable/disable actions.
func newAddressField(resolverFn func() *ens.Resolver, onChange func()) *addressField {
	f := &addressField{
		entry:      widget.NewEntry(),
		status:     canvas.NewText("", colorNeutral),
		resolverFn: resolverFn,
		onChange:   onChange,
	}
	f.status.TextStyle = fyne.TextStyle{Monospace: true} // resolved addresses read as mono
	f.entry.SetPlaceHolder("0x… address or ENS name")
	f.entry.OnChanged = f.onEntryChanged
	return f
}

// container returns the composed widget (entry above its status line).
func (f *addressField) container() fyne.CanvasObject {
	return container.NewVBox(f.entry, f.status)
}

// Address returns the resolved address and whether the field currently holds a
// valid address or successfully-resolved ENS name.
func (f *addressField) Address() (common.Address, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolved, f.valid
}

// Set programmatically sets the text (e.g. a "self" shortcut) and validates it.
func (f *addressField) Set(text string) {
	f.entry.SetText(text)
}

func (f *addressField) setStatus(msg string, c color.Color) {
	f.status.Text = msg
	f.status.Color = c
	f.status.Refresh()
}

func (f *addressField) setResult(addr common.Address, valid bool) {
	f.mu.Lock()
	f.resolved, f.valid = addr, valid
	f.mu.Unlock()
	if f.onChange != nil {
		f.onChange()
	}
}

// onEntryChanged validates synchronously for plain addresses and kicks off a
// debounced background resolution for ENS names.
func (f *addressField) onEntryChanged(text string) {
	f.mu.Lock()
	f.gen++
	gen := f.gen
	f.mu.Unlock()

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		f.setResult(common.Address{}, false)
		f.setStatus("", colorNeutral)
		return
	}

	if ens.LooksLikeENS(trimmed) {
		f.setResult(common.Address{}, false)
		f.setStatus("resolving "+trimmed+"…", colorNeutral)
		go f.resolveENS(gen, trimmed)
		return
	}

	// Plain address path — validate synchronously.
	addr, err := address.Parse(trimmed)
	switch err {
	case nil:
		f.setResult(addr, true)
		f.setStatus("✓ "+address.Format(addr), colorOK)
	case address.ErrBadChecksum:
		f.setResult(common.Address{}, false)
		f.setStatus("✗ invalid EIP-55 checksum — check for a typo", colorError)
	default:
		f.setResult(common.Address{}, false)
		f.setStatus("enter a hex address or ENS name", colorNeutral)
	}
}

// resolveENS debounces then forward-resolves an ENS name, guarding against stale
// results with the generation counter. UI mutations are marshalled via fyne.Do.
func (f *addressField) resolveENS(gen int, name string) {
	time.Sleep(resolveDebounce)
	if f.staleGen(gen) {
		return
	}

	resolver := f.resolverFn()
	if resolver == nil {
		fyne.Do(func() {
			if f.staleGen(gen) {
				return
			}
			f.setStatus("connect an RPC to resolve ENS names", colorNeutral)
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	addr, err := resolver.Resolve(ctx, name)

	fyne.Do(func() {
		if f.staleGen(gen) {
			return // input changed while resolving; ignore this result
		}
		if err != nil {
			f.setResult(common.Address{}, false)
			f.setStatus("✗ "+name+" does not resolve", colorError)
			return
		}
		f.setResult(addr, true)
		f.setStatus("✓ "+name+" → "+address.Short(addr), colorOK)
	})
}

// staleGen reports whether a newer edit has superseded generation gen.
func (f *addressField) staleGen(gen int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return gen != f.gen
}
