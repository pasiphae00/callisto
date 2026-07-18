package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/signer"
	"codeberg.org/pasiphae/callisto/internal/signer/hardware"
	"codeberg.org/pasiphae/callisto/internal/signer/hot"
	"codeberg.org/pasiphae/callisto/internal/wallet"

	"github.com/ethereum/go-ethereum/common"
)

// walletsPane manages the persisted wallet registry and the live signer session.
// Descriptors (address + derivation path, no secrets) persist; unlocking a wallet
// creates an in-memory signer that the rest of the app uses to sign, and which is
// wiped on lock / disconnect / app close.
type walletsPane struct {
	app *App

	list       *widget.List
	unlockBtn  *widget.Button
	lockBtn    *widget.Button
	removeBtn  *widget.Button
	selected   int
	detailAddr *widget.Entry // full, selectable/copyable address of the selected wallet
	detailVal  string        // canonical value detailAddr should show; edits revert to this
	detailBox  *fyne.Container
}

func newWalletsPane(a *App) *walletsPane {
	return &walletsPane{app: a, selected: -1}
}

func (p *walletsPane) build() fyne.CanvasObject {
	p.list = widget.NewList(
		func() int { return len(p.app.cfg.Wallets) },
		func() fyne.CanvasObject {
			dot := canvas.NewText("●", statusGray)
			return container.NewHBox(dot, monoLabel("template"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			w := p.app.cfg.Wallets[i]
			row := o.(*fyne.Container)
			dot := row.Objects[0].(*canvas.Text)
			label := row.Objects[1].(*widget.Label)

			if p.app.cfg.ActiveWallet == w.ID {
				dot.Color = statusGreen
			} else {
				dot.Color = statusGray
			}
			dot.Refresh()
			label.SetText(p.rowLabel(w))
		},
	)
	p.list.OnSelected = func(id widget.ListItemID) { p.selected = id; p.updateButtons() }
	p.list.OnUnselected = func(widget.ListItemID) { p.selected = -1; p.updateButtons() }

	addBtn := widget.NewButton("Add hot wallet…", p.showAddHotWallet)
	addHwBtn := widget.NewButton("Add hardware…", p.showAddHardwareWallet)
	p.unlockBtn = widget.NewButton("Unlock", p.unlockSelected)
	p.lockBtn = widget.NewButton("Lock", p.lockActive)
	p.removeBtn = widget.NewButton("Remove", p.removeSelected)

	header := widget.NewLabelWithStyle("Wallets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("Add a wallet from a seed phrase (held in memory only while unlocked, wiped on lock/exit) or a hardware device (keys never leave the device). Nothing secret is written to disk.")
	help.Wrapping = fyne.TextWrapWord
	buttons := container.NewHBox(addBtn, addHwBtn, p.unlockBtn, p.lockBtn, p.removeBtn)

	p.detailBox = p.buildDetailBox()
	p.updateButtons() // also populates the detail box for the initial (no-selection) state

	top := container.NewVBox(header, help, buttons, widget.NewSeparator())
	return container.NewBorder(top, p.detailBox, nil, nil, p.list)
}

// buildDetailBox builds the full-address detail bar shown below the list: a
// read-only, selectable Entry (so the address is copyable by selection as well
// as the explicit button) plus a Copy button. Elsewhere in the app addresses
// are shown short; this is deliberately the one place the full address is
// always visible.
func (p *walletsPane) buildDetailBox() *fyne.Container {
	p.detailAddr = widget.NewEntry()
	p.detailAddr.TextStyle = fyne.TextStyle{Monospace: true}
	// Deliberately left enabled (not Disable()d): a disabled widget blocks all
	// interaction in Fyne, including mouse-drag text selection, which would
	// defeat "copyable by text selection." Instead, revert any edit back to
	// detailVal so it's read-only in effect while staying fully
	// selectable/copyable. setDetailAddr (below) is the only legitimate writer.
	p.detailAddr.OnChanged = func(s string) {
		if s != p.detailVal {
			p.detailAddr.SetText(p.detailVal)
		}
	}
	copyBtn := widget.NewButton("Copy", func() {
		if p.detailAddr.Text != "" {
			p.app.fyneApp.Clipboard().SetContent(p.detailAddr.Text)
		}
	})
	row := container.NewBorder(nil, nil, widget.NewLabel("Address:"), copyBtn, p.detailAddr)
	return container.NewVBox(widget.NewSeparator(), row)
}

// rowLabel renders a wallet row's text: lock state, label, short address, and
// kind. The active-selection indicator is a separately colored dot (see build),
// kept as a genuine colored glyph rather than an emoji; the lock/unlock icons
// stay as-is (requested to keep them — they read fine, unlike the green-circle
// emoji they're not being confused with a status color).
func (p *walletsPane) rowLabel(w wallet.Descriptor) string {
	icon := "🔒"
	if _, id, ok := p.app.currentSigner(); ok && id == w.ID {
		icon = "🔓"
	}
	name := w.Label
	if name == "" {
		name = "(unnamed)"
	}
	short := w.Address
	if a, err := address.Parse(w.Address); err == nil {
		short = address.Short(a)
	}
	return fmt.Sprintf("%s  %s — %s  [%s]", icon, name, short, w.Kind)
}

// setDetailAddr updates the full-address detail box (see buildDetailBox) to s,
// the only legitimate way its text changes.
func (p *walletsPane) setDetailAddr(s string) {
	p.detailVal = s
	if p.detailAddr != nil {
		p.detailAddr.SetText(s)
	}
}

func (p *walletsPane) updateButtons() {
	has := p.selected >= 0 && p.selected < len(p.app.cfg.Wallets)
	_, activeID, unlocked := p.app.currentSigner()
	if has {
		p.removeBtn.Enable()
		w := p.app.cfg.Wallets[p.selected]
		if unlocked && activeID == w.ID {
			p.unlockBtn.Disable()
		} else {
			p.unlockBtn.Enable()
		}
		if a, err := address.Parse(w.Address); err == nil {
			p.setDetailAddr(address.Format(a))
		} else {
			p.setDetailAddr(w.Address)
		}
	} else {
		p.removeBtn.Disable()
		p.unlockBtn.Disable()
		p.setDetailAddr("")
	}
	if unlocked {
		p.lockBtn.Enable()
	} else {
		p.lockBtn.Disable()
	}
}

// refresh redraws the list and buttons and updates the shared status bar.
func (p *walletsPane) refresh() {
	p.list.Refresh()
	p.updateButtons()
	p.app.refreshStatusBar()
}

// showAddHotWallet collects a label, mnemonic, optional passphrase, and account
// index, derives the wallet, persists the descriptor, and holds the live signer.
func (p *walletsPane) showAddHotWallet() {
	label := widget.NewEntry()
	label.SetPlaceHolder("e.g. Main")
	mnemonic := widget.NewMultiLineEntry()
	mnemonic.SetPlaceHolder("12 or 24 word recovery phrase")
	mnemonic.Wrapping = fyne.TextWrapWord
	passphrase := widget.NewPasswordEntry()
	passphrase.SetPlaceHolder("optional BIP-39 passphrase")
	index := widget.NewEntry()
	index.SetText("0")

	items := []*widget.FormItem{
		widget.NewFormItem("Label", label),
		widget.NewFormItem("Recovery phrase", mnemonic),
		widget.NewFormItem("Passphrase", passphrase),
		widget.NewFormItem("Account #", index),
	}
	d := dialog.NewForm("Add hot wallet", "Add", "Cancel", items, func(ok bool) {
		// Best-effort: drop the reference to the entered phrase text.
		defer mnemonic.SetText("")
		if !ok {
			return
		}
		idx, err := strconv.ParseUint(index.Text, 10, 32)
		if err != nil {
			dialog.ShowError(fmt.Errorf("account # must be a non-negative integer"), p.app.window)
			return
		}
		w, err := hot.Open(mnemonic.Text, passphrase.Text, hot.DefaultPath(uint32(idx)))
		if err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		desc := wallet.Descriptor{
			ID:             newWalletID(),
			Label:          label.Text,
			Address:        address.Format(w.Address()),
			Kind:           wallet.KindHot,
			DerivationPath: hot.DefaultPath(uint32(idx)),
		}
		if err := p.persistAndActivate(desc, w); err != nil {
			w.Lock()
			dialog.ShowError(err, p.app.window)
			return
		}
		p.refresh()
		dialog.ShowInformation("Wallet added",
			fmt.Sprintf("%s\n%s", desc.Label, desc.Address), p.app.window)
	}, p.app.window)
	d.Resize(fyne.NewSize(560, 380))
	d.Show()
}

// showAddHardwareWallet connects a Ledger or Trezor, derives an account, and
// registers it. The device must be connected and unlocked (Ledger: Ethereum app
// open). Signing later happens on the device.
func (p *walletsPane) showAddHardwareWallet() {
	label := widget.NewEntry()
	label.SetPlaceHolder("e.g. Ledger 1")
	deviceSel := widget.NewSelect([]string{"Ledger", "Trezor"}, nil)
	deviceSel.SetSelected("Ledger")
	index := widget.NewEntry()
	index.SetText("0")
	passphrase := widget.NewPasswordEntry()
	passphrase.SetPlaceHolder("Trezor only: leave blank for your standard wallet")

	items := []*widget.FormItem{
		widget.NewFormItem("Label", label),
		widget.NewFormItem("Device", deviceSel),
		widget.NewFormItem("Account #", index),
		widget.NewFormItem("Passphrase", passphrase),
	}
	d := dialog.NewForm("Add hardware wallet", "Connect", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		idx, err := strconv.ParseUint(index.Text, 10, 32)
		if err != nil {
			dialog.ShowError(fmt.Errorf("account # must be a non-negative integer"), p.app.window)
			return
		}
		kind := hardwareKind(deviceSel.Selected)
		labelText := label.Text
		pass := passphrase.Text

		progress := dialog.NewCustomWithoutButtons("Connecting…",
			widget.NewLabel("Confirm on your "+deviceSel.Selected+" if prompted."), p.app.window)
		progress.Show()
		go func() {
			s, err := hardware.Open(kind, uint32(idx), pass)
			fyne.Do(func() {
				progress.Hide()
				if err != nil {
					dialog.ShowError(err, p.app.window)
					return
				}
				desc := wallet.Descriptor{
					ID:             newWalletID(),
					Label:          labelText,
					Address:        address.Format(s.Address()),
					Kind:           wallet.SignerKind(kind),
					DerivationPath: hardwarePath(uint32(idx)),
				}
				if err := p.persistAndActivate(desc, s); err != nil {
					s.Lock()
					dialog.ShowError(err, p.app.window)
					return
				}
				p.refresh()
				dialog.ShowInformation("Hardware wallet added",
					fmt.Sprintf("%s\n%s", desc.Label, desc.Address), p.app.window)
			})
		}()
	}, p.app.window)
	d.Resize(fyne.NewSize(480, 300))
	d.Show()
}

// unlockSelected re-derives the selected wallet from a freshly entered phrase and,
// only if the derived address matches the stored descriptor, installs the signer.
// Hardware wallets reconnect the device instead of prompting for a phrase.
func (p *walletsPane) unlockSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Wallets) {
		return
	}
	desc := p.app.cfg.Wallets[p.selected]

	if desc.IsHardware() {
		p.unlockHardware(desc)
		return
	}

	mnemonic := widget.NewMultiLineEntry()
	mnemonic.SetPlaceHolder("recovery phrase for " + desc.Address)
	mnemonic.Wrapping = fyne.TextWrapWord
	passphrase := widget.NewPasswordEntry()
	passphrase.SetPlaceHolder("optional BIP-39 passphrase")

	items := []*widget.FormItem{
		widget.NewFormItem("Recovery phrase", mnemonic),
		widget.NewFormItem("Passphrase", passphrase),
	}
	d := dialog.NewForm("Unlock "+displayName(desc), "Unlock", "Cancel", items, func(ok bool) {
		defer mnemonic.SetText("")
		if !ok {
			return
		}
		path := desc.DerivationPath
		if path == "" {
			path = hot.DefaultPath(0)
		}
		w, err := hot.Open(mnemonic.Text, passphrase.Text, path)
		if err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		// The phrase must reproduce exactly the stored address.
		if !addrEqual(w.Address(), desc.Address) {
			w.Lock()
			dialog.ShowError(fmt.Errorf("this phrase/account derives a different address than this wallet"), p.app.window)
			return
		}
		p.app.setSigner(desc.ID, w)
		p.app.cfg.ActiveWallet = desc.ID
		if err := p.app.cfg.Save(); err != nil {
			dialog.ShowError(err, p.app.window)
		}
		p.refresh()
	}, p.app.window)
	d.Resize(fyne.NewSize(560, 300))
	d.Show()
}

// unlockHardware reconnects a hardware device for a saved descriptor and installs
// the signer only if the device reproduces the stored address. For a Trezor
// hidden wallet, the same passphrase used originally must be re-entered — the
// wrong (or missing) one simply derives a different address, caught below.
func (p *walletsPane) unlockHardware(desc wallet.Descriptor) {
	kind := signer.Kind(desc.Kind)
	path := desc.DerivationPath
	if path == "" {
		path = hot.DefaultPath(0)
	}

	passphrase := widget.NewPasswordEntry()
	passphrase.SetPlaceHolder("Trezor hidden wallet passphrase, if any")
	d := dialog.NewForm("Unlock "+displayName(desc), "Connect", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Passphrase", passphrase)},
		func(ok bool) {
			if !ok {
				return
			}
			p.connectHardware(desc, kind, path, passphrase.Text)
		}, p.app.window)
	d.Resize(fyne.NewSize(480, 180))
	d.Show()
}

// connectHardware performs the actual device connection for unlockHardware.
func (p *walletsPane) connectHardware(desc wallet.Descriptor, kind signer.Kind, path, passphrase string) {
	progress := dialog.NewCustomWithoutButtons("Connecting…",
		widget.NewLabel("Confirm on your device if prompted."), p.app.window)
	progress.Show()
	go func() {
		s, err := hardware.OpenPath(kind, path, passphrase)
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			if !addrEqual(s.Address(), desc.Address) {
				s.Lock()
				dialog.ShowError(fmt.Errorf("the device (and passphrase, if any) derived a different address than this wallet"), p.app.window)
				return
			}
			p.app.setSigner(desc.ID, s)
			p.app.cfg.ActiveWallet = desc.ID
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
			}
			p.refresh()
		})
	}()
}

// lockActive wipes the current signer session.
func (p *walletsPane) lockActive() {
	p.app.clearSigner()
	p.refresh()
}

// removeSelected deletes a wallet descriptor (locking it first if it is active).
func (p *walletsPane) removeSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Wallets) {
		return
	}
	desc := p.app.cfg.Wallets[p.selected]
	dialog.ShowConfirm("Remove wallet",
		fmt.Sprintf("Remove %q?\nThis only forgets the address/label — your seed phrase is your backup.", displayName(desc)),
		func(ok bool) {
			if !ok {
				return
			}
			if _, id, active := p.app.currentSigner(); active && id == desc.ID {
				p.app.clearSigner()
			}
			p.app.cfg.RemoveWallet(desc.ID)
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
			}
			p.selected = -1
			p.list.UnselectAll()
			p.refresh()
		}, p.app.window)
}

// persistAndActivate saves a new descriptor, marks it active, and installs the
// signer. On a persistence failure the caller locks the signer.
func (p *walletsPane) persistAndActivate(desc wallet.Descriptor, s signer.Signer) error {
	if err := p.app.cfg.UpsertWallet(desc); err != nil {
		return err
	}
	p.app.cfg.ActiveWallet = desc.ID
	if err := p.app.cfg.Save(); err != nil {
		p.app.cfg.RemoveWallet(desc.ID)
		return err
	}
	p.app.setSigner(desc.ID, s)
	return nil
}

// --- helpers ---------------------------------------------------------------

func displayName(w wallet.Descriptor) string {
	if w.Label != "" {
		return w.Label
	}
	return w.Address
}

// addrEqual compares a derived address to a stored (string) address, tolerant of
// checksum/case differences.
func addrEqual(a common.Address, stored string) bool {
	parsed, err := address.Parse(stored)
	if err != nil {
		return false
	}
	return a == parsed
}

// hardwareKind maps a device-picker label to a signer kind.
func hardwareKind(sel string) signer.Kind {
	if sel == "Trezor" {
		return signer.KindTrezor
	}
	return signer.KindLedger
}

// hardwarePath is the standard BIP-44 path used for hardware accounts.
func hardwarePath(index uint32) string {
	return hot.DefaultPath(index)
}

// newWalletID returns a short random identifier for a wallet descriptor.
func newWalletID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failing is fatal-adjacent; fall back to a fixed prefix so the
		// caller still gets a usable (if not unique) id rather than a panic.
		return "wallet-" + hex.EncodeToString(b[:])
	}
	return hex.EncodeToString(b[:])
}
