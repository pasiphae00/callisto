package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
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

	list      *widget.List
	unlockBtn *widget.Button
	lockBtn   *widget.Button
	removeBtn *widget.Button
	selected  int
}

func newWalletsPane(a *App) *walletsPane {
	return &walletsPane{app: a, selected: -1}
}

func (p *walletsPane) build() fyne.CanvasObject {
	p.list = widget.NewList(
		func() int { return len(p.app.cfg.Wallets) },
		func() fyne.CanvasObject { return widget.NewLabel("template") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(p.rowLabel(p.app.cfg.Wallets[i]))
		},
	)
	p.list.OnSelected = func(id widget.ListItemID) { p.selected = id; p.updateButtons() }
	p.list.OnUnselected = func(widget.ListItemID) { p.selected = -1; p.updateButtons() }

	addBtn := widget.NewButton("Add hot wallet…", p.showAddHotWallet)
	addHwBtn := widget.NewButton("Add hardware…", p.showAddHardwareWallet)
	p.unlockBtn = widget.NewButton("Unlock", p.unlockSelected)
	p.lockBtn = widget.NewButton("Lock", p.lockActive)
	p.removeBtn = widget.NewButton("Remove", p.removeSelected)
	p.updateButtons()

	header := widget.NewLabelWithStyle("Wallets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("Add a wallet from a seed phrase (held in memory only while unlocked, wiped on lock/exit) or a hardware device (keys never leave the device). Nothing secret is written to disk.")
	help.Wrapping = fyne.TextWrapWord
	buttons := container.NewHBox(addBtn, addHwBtn, p.unlockBtn, p.lockBtn, p.removeBtn)

	top := container.NewVBox(header, help, buttons, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, p.list)
}

// rowLabel renders a wallet row: lock state, label, short address, and whether
// it is the active selection.
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
	active := ""
	if p.app.cfg.ActiveWallet == w.ID {
		active = "  ●"
	}
	return fmt.Sprintf("%s  %s — %s  [%s]%s", icon, name, short, w.Kind, active)
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
	} else {
		p.removeBtn.Disable()
		p.unlockBtn.Disable()
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

	items := []*widget.FormItem{
		widget.NewFormItem("Label", label),
		widget.NewFormItem("Device", deviceSel),
		widget.NewFormItem("Account #", index),
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

		progress := dialog.NewCustomWithoutButtons("Connecting…",
			widget.NewLabel("Confirm on your "+deviceSel.Selected+" if prompted."), p.app.window)
		progress.Show()
		go func() {
			s, err := hardware.Open(kind, uint32(idx))
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
// the signer only if the device reproduces the stored address.
func (p *walletsPane) unlockHardware(desc wallet.Descriptor) {
	kind := signer.Kind(desc.Kind)
	path := desc.DerivationPath
	if path == "" {
		path = hot.DefaultPath(0)
	}
	progress := dialog.NewCustomWithoutButtons("Connecting…",
		widget.NewLabel("Confirm on your device if prompted."), p.app.window)
	progress.Show()
	go func() {
		s, err := hardware.OpenPath(kind, path)
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			if !addrEqual(s.Address(), desc.Address) {
				s.Lock()
				dialog.ShowError(fmt.Errorf("the device derived a different address than this wallet"), p.app.window)
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
