package ui

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/config"
	"codeberg.org/pasiphae/callisto/internal/keystore"
	"codeberg.org/pasiphae/callisto/internal/signer"
	"codeberg.org/pasiphae/callisto/internal/signer/hardware"
	"codeberg.org/pasiphae/callisto/internal/signer/hot"
	"codeberg.org/pasiphae/callisto/internal/wallet"

	"github.com/ethereum/go-ethereum/common"
)

// minKeystorePassphraseLen is a soft minimum for the wallet-encryption passphrase.
// The seed phrase remains the real backup, but the passphrase guards the on-disk
// keystore, so we nudge users away from trivially short ones.
const minKeystorePassphraseLen = 8

// walletsPane manages the persisted wallet registry and the live signer session.
// Descriptors (address + derivation path, no secrets) persist; unlocking a wallet
// creates an in-memory signer that the rest of the app uses to sign, and which is
// wiped on lock / disconnect / app close.
type walletsPane struct {
	app *App

	list       *widget.List
	unlockBtn  *widget.Button
	lockBtn    *widget.Button
	renameBtn  *widget.Button
	removeBtn  *widget.Button
	selected   int
	detailAddr *widget.Entry // full, selectable/copyable address of the selected wallet
	detailVal  string        // canonical value detailAddr should show; edits revert to this
	detailBox  *fyne.Container
}

func newWalletsPane(a *App) *walletsPane {
	return &walletsPane{app: a, selected: -1}
}

// walletRow is a wallets-list row: a single tap selects it (driven explicitly,
// since a custom tappable widget consumes the tap the List would otherwise use for
// selection) and a double tap activates the wallet.
type walletRow struct {
	widget.BaseWidget
	dot         *canvas.Text
	label       *widget.Label
	onTap       func()
	onDoubleTap func()
}

func newWalletRow() *walletRow {
	r := &walletRow{
		dot:   canvas.NewText("●", statusGray),
		label: monoLabel("template"),
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *walletRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewHBox(r.dot, r.label))
}

// Tapped selects the wallet this row represents.
func (r *walletRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

// DoubleTapped activates the wallet this row represents.
func (r *walletRow) DoubleTapped(*fyne.PointEvent) {
	if r.onDoubleTap != nil {
		r.onDoubleTap()
	}
}

// activateWallet makes the wallet at index id the active one and persists it.
func (p *walletsPane) activateWallet(id widget.ListItemID) {
	if id < 0 || id >= len(p.app.cfg.Wallets) {
		return
	}
	w := p.app.cfg.Wallets[id]
	if p.app.cfg.ActiveWallet == w.ID {
		return // already active
	}
	p.app.cfg.ActiveWallet = w.ID
	if err := p.app.cfg.Save(); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	p.selected = id
	p.list.Select(id)
	p.refresh()
}

func (p *walletsPane) build() fyne.CanvasObject {
	p.list = widget.NewList(
		func() int { return len(p.app.cfg.Wallets) },
		func() fyne.CanvasObject {
			return newWalletRow()
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			w := p.app.cfg.Wallets[i]
			row := o.(*walletRow)

			if p.app.cfg.ActiveWallet == w.ID {
				row.dot.Color = statusGreen
			} else {
				row.dot.Color = statusGray
			}
			row.dot.Refresh()
			row.label.SetText(p.rowLabel(w))
			row.onTap = func() { p.list.Select(i) }        // single-click selects
			row.onDoubleTap = func() { p.activateWallet(i) } // double-click activates
		},
	)
	p.list.OnSelected = func(id widget.ListItemID) { p.selected = id; p.updateButtons() }
	p.list.OnUnselected = func(widget.ListItemID) { p.selected = -1; p.updateButtons() }

	addBtn := widget.NewButton("Add hot wallet…", p.showAddHotWallet)
	addHwBtn := widget.NewButton("Add hardware…", p.showAddHardwareWallet)
	p.unlockBtn = widget.NewButton("Unlock", p.unlockSelected)
	p.lockBtn = widget.NewButton("Lock", p.lockActive)
	p.renameBtn = widget.NewButton("Rename", p.renameSelected)
	p.removeBtn = widget.NewButton("Remove", p.removeSelected)

	header := widget.NewLabelWithStyle("Wallets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("Add a hot wallet (its seed is encrypted with your passphrase and stored locally; decrypted in memory only while unlocked, wiped on lock/exit) or a hardware device (keys never leave the device). Double-click a wallet to make it active.")
	help.Wrapping = fyne.TextWrapWord
	buttons := container.NewHBox(addBtn, addHwBtn, p.unlockBtn, p.lockBtn, p.renameBtn, p.removeBtn)

	p.detailBox = p.buildDetailBox()
	p.updateButtons() // also populates the detail box for the initial (no-selection) state

	top := container.NewVBox(header, help, indentToText(buttons), widget.NewSeparator())
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
		p.renameBtn.Enable()
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
		p.renameBtn.Disable()
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

// showAddHotWallet runs the one-time hot-wallet import: enter the recovery phrase
// once, pick the account(s) to add from a derived index→address list, and set an
// encryption passphrase. The seed is then stored only as an encrypted keystore;
// subsequent unlocks need just the passphrase (see unlockHotKeystore).
func (p *walletsPane) showAddHotWallet() {
	labelPrefix := widget.NewEntry()
	labelPrefix.SetPlaceHolder("e.g. Main")
	mnemonic := widget.NewMultiLineEntry()
	mnemonic.SetPlaceHolder("12 or 24 word recovery phrase")
	mnemonic.Wrapping = fyne.TextWrapWord
	bip39pass := widget.NewPasswordEntry()
	bip39pass.SetPlaceHolder("optional BIP-39 passphrase (25th word)")
	ksPass := widget.NewPasswordEntry()
	ksPass.SetPlaceHolder("passphrase to encrypt this wallet")
	ksPass2 := widget.NewPasswordEntry()
	ksPass2.SetPlaceHolder("confirm passphrase")

	// Account-selection state, populated on demand from the phrase.
	var checks []*widget.Check
	var accounts []hot.Account
	accountsBox := container.NewVBox()
	var shown uint32
	hint := widget.NewLabel("Enter your phrase, then Load accounts to pick which to add.")
	hint.Wrapping = fyne.TextWrapWord

	loadMore := func() {
		got, err := hot.PreviewAccounts(mnemonic.Text, bip39pass.Text, shown, 10)
		if err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		for _, a := range got {
			label := fmt.Sprintf("#%d   %s", a.Index, address.Format(a.Address))
			chk := widget.NewCheck(label, nil)
			if a.Index == 0 {
				chk.SetChecked(true) // sensible default
			}
			chk.Refresh()
			checks = append(checks, chk)
			accounts = append(accounts, a)
			accountsBox.Add(chk)
		}
		shown += uint32(len(got))
		hint.SetText("Tick the account(s) to add. Show more to reveal higher indexes.")
		accountsBox.Refresh()
	}
	loadBtn := widget.NewButton("Load accounts", func() {
		accountsBox.Objects = nil
		checks, accounts, shown = nil, nil, 0
		loadMore()
	})
	moreBtn := widget.NewButton("Show more", loadMore)

	form := container.NewVBox(
		widget.NewLabelWithStyle("Label prefix", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		labelPrefix,
		widget.NewLabelWithStyle("Recovery phrase (one-time import)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		mnemonic,
		widget.NewLabelWithStyle("BIP-39 passphrase (optional)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		bip39pass,
		container.NewHBox(loadBtn, moreBtn),
		hint,
		accountsBox,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Encryption passphrase (used to unlock later)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		ksPass,
		ksPass2,
	)

	d := dialog.NewCustomConfirm("Import hot wallet", "Import", "Cancel",
		container.NewVScroll(form), func(okBtn bool) {
			defer mnemonic.SetText("")
			if !okBtn {
				return
			}
			p.doImportHot(labelPrefix.Text, mnemonic.Text, bip39pass.Text, ksPass.Text, ksPass2.Text, checks, accounts)
		}, p.app.window)
	d.Resize(fyne.NewSize(620, 640))
	d.Show()
}

// doImportHot validates the import inputs, encrypts the seed to a shared keystore,
// and creates one descriptor per selected account.
func (p *walletsPane) doImportHot(labelPrefix, mnemonic, bip39pass, ksPass, ksPass2 string, checks []*widget.Check, accounts []hot.Account) {
	var selected []hot.Account
	for i, c := range checks {
		if c.Checked && i < len(accounts) {
			selected = append(selected, accounts[i])
		}
	}
	if len(selected) == 0 {
		dialog.ShowError(fmt.Errorf("load accounts and tick at least one to import"), p.app.window)
		return
	}
	if len(ksPass) < minKeystorePassphraseLen {
		dialog.ShowError(fmt.Errorf("choose an encryption passphrase of at least %d characters", minKeystorePassphraseLen), p.app.window)
		return
	}
	if ksPass != ksPass2 {
		dialog.ShowError(fmt.Errorf("the encryption passphrases do not match"), p.app.window)
		return
	}

	// Encrypt the seed once (also validates the mnemonic).
	ks, err := hot.NewKeystore(mnemonic, bip39pass, ksPass)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	ksDir, err := config.KeystoreDir()
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	ksID := newWalletID()
	if err := keystore.Save(filepath.Join(ksDir, ksID+".json"), ks); err != nil {
		dialog.ShowError(fmt.Errorf("save keystore: %w", err), p.app.window)
		return
	}

	prefix := labelPrefix
	if prefix == "" {
		prefix = "Wallet"
	}
	created := 0
	for _, a := range selected {
		desc := wallet.Descriptor{
			ID:             newWalletID(),
			Label:          fmt.Sprintf("%s #%d", prefix, a.Index),
			Address:        address.Format(a.Address),
			Kind:           wallet.KindHot,
			DerivationPath: a.Path,
			KeystoreID:     ksID,
		}
		if err := p.app.cfg.UpsertWallet(desc); err != nil {
			continue
		}
		if created == 0 {
			p.app.cfg.ActiveWallet = desc.ID
		}
		created++
	}
	if created == 0 {
		_ = keystore.Wipe(filepath.Join(ksDir, ksID+".json"))
		dialog.ShowError(fmt.Errorf("no accounts could be imported"), p.app.window)
		return
	}
	if err := p.app.cfg.Save(); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	p.refresh()
	dialog.ShowInformation("Wallet imported",
		fmt.Sprintf("%d account(s) imported and encrypted.\nUnlock with your passphrase — no recovery phrase needed again.", created),
		p.app.window)
}

// showAddHardwareWallet connects a Ledger or Trezor, derives an account, and
// registers it. The device must be connected and unlocked (Ledger: Ethereum app
// open). Signing later happens on the device.
func (p *walletsPane) showAddHardwareWallet() {
	label := widget.NewEntry()
	label.SetPlaceHolder("e.g. Ledger 1")
	deviceSel := widget.NewSelect([]string{"Ledger", "Trezor"}, nil)
	index := widget.NewEntry()
	index.SetText("0")
	passphrase := widget.NewPasswordEntry()
	passphrase.SetPlaceHolder("leave blank for your standard wallet")

	// Passphrase applies only to Trezor (hidden wallets); show its row only when
	// Trezor is selected.
	passLabel := widget.NewLabelWithStyle("Passphrase", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	setPassphraseVisible := func(sel string) {
		if sel == "Trezor" {
			passLabel.Show()
			passphrase.Show()
		} else {
			passLabel.Hide()
			passphrase.Hide()
		}
	}
	deviceSel.OnChanged = setPassphraseVisible
	deviceSel.SetSelected("Ledger") // triggers OnChanged → hides passphrase

	bold := func(s string) *widget.Label {
		return widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	grid := container.New(layout.NewFormLayout(),
		bold("Label"), label,
		bold("Device"), deviceSel,
		bold("Account #"), index,
		passLabel, passphrase,
	)

	d := dialog.NewCustomConfirm("Add hardware wallet", "Connect", "Cancel", grid, func(ok bool) {
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

// unlockSelected installs a live signer for the selected wallet. Hardware wallets
// reconnect the device; hot wallets with an encrypted keystore unlock with just
// their passphrase; legacy hot wallets (imported before keystores) fall back to
// re-entering the recovery phrase.
func (p *walletsPane) unlockSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Wallets) {
		return
	}
	desc := p.app.cfg.Wallets[p.selected]

	switch {
	case desc.IsHardware():
		p.unlockHardware(desc)
	case desc.KeystoreID != "":
		p.unlockHotKeystore(desc)
	default:
		p.unlockHotLegacy(desc)
	}
}

// unlockHotKeystore unlocks an encrypted hot wallet with its passphrase only.
func (p *walletsPane) unlockHotKeystore(desc wallet.Descriptor) {
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder("wallet passphrase")
	d := dialog.NewForm("Unlock "+displayName(desc), "Unlock", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Passphrase", pass)},
		func(ok bool) {
			if !ok {
				return
			}
			p.openFromKeystore(desc, pass.Text)
		}, p.app.window)
	d.Resize(fyne.NewSize(460, 180))
	d.Show()
}

// openFromKeystore decrypts the wallet's keystore off the UI thread (scrypt is
// deliberately slow) and installs the signer only if the derived address matches.
func (p *walletsPane) openFromKeystore(desc wallet.Descriptor, pass string) {
	ksDir, err := config.KeystoreDir()
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	ks, err := keystore.Load(filepath.Join(ksDir, desc.KeystoreID+".json"))
	if err != nil {
		dialog.ShowError(fmt.Errorf("keystore not found for this wallet: %w", err), p.app.window)
		return
	}
	path := desc.DerivationPath
	if path == "" {
		path = hot.DefaultPath(0)
	}

	progress := dialog.NewCustomWithoutButtons("Unlocking…",
		widget.NewLabel("Decrypting keystore…"), p.app.window)
	progress.Show()
	go func() {
		w, err := hot.OpenFromKeystore(ks, pass, path)
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				if errors.Is(err, keystore.ErrBadPassphrase) {
					dialog.ShowError(fmt.Errorf("wrong passphrase"), p.app.window)
				} else {
					dialog.ShowError(err, p.app.window)
				}
				return
			}
			if !addrEqual(w.Address(), desc.Address) {
				w.Lock()
				dialog.ShowError(fmt.Errorf("the keystore derived a different address than this wallet"), p.app.window)
				return
			}
			p.app.setSigner(desc.ID, w)
			p.app.cfg.ActiveWallet = desc.ID
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
			}
			p.refresh()
		})
	}()
}

// unlockHotLegacy unlocks a pre-keystore hot wallet by re-entering the recovery
// phrase (the old flow), kept for wallets imported before encrypted keystores.
func (p *walletsPane) unlockHotLegacy(desc wallet.Descriptor) {
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

	// Only Trezor has hidden-wallet passphrases; Ledger has no such concept, so
	// skip the prompt entirely and reconnect directly.
	if kind != signer.KindTrezor {
		p.connectHardware(desc, kind, path, "")
		return
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

// renameSelected changes the label of the selected wallet. Only the display
// label changes — the address, derivation path, and any keystore are untouched.
func (p *walletsPane) renameSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Wallets) {
		return
	}
	desc := p.app.cfg.Wallets[p.selected]

	entry := widget.NewEntry()
	entry.SetText(desc.Label)
	entry.SetPlaceHolder("wallet label")
	d := dialog.NewForm("Rename wallet", "Save", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Label", entry)},
		func(ok bool) {
			if !ok {
				return
			}
			name := strings.TrimSpace(entry.Text)
			if name == "" {
				dialog.ShowError(fmt.Errorf("label cannot be empty"), p.app.window)
				return
			}
			if name == desc.Label {
				return
			}
			desc.Label = name
			if err := p.app.cfg.UpsertWallet(desc); err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			p.refresh()
		}, p.app.window)
	d.Resize(fyne.NewSize(460, 180))
	d.Show()
}

// removeSelected deletes a wallet descriptor (locking it first if it is active).
// For an encrypted hot wallet, the encrypted keystore file is securely wiped once
// no remaining wallet references it (accounts from one import share a keystore).
func (p *walletsPane) removeSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Wallets) {
		return
	}
	desc := p.app.cfg.Wallets[p.selected]

	msg := fmt.Sprintf("Remove %q?\nThis only forgets the address/label — your recovery phrase is your backup.", displayName(desc))
	if desc.KeystoreID != "" && p.lastKeystoreReference(desc) {
		msg = fmt.Sprintf("Remove %q?\nThis deletes its encrypted keystore from this device (no other account uses it). Your recovery phrase remains your backup.", displayName(desc))
	}
	dialog.ShowConfirm("Remove wallet", msg,
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
			p.maybeWipeKeystore(desc)
			p.selected = -1
			p.list.UnselectAll()
			p.refresh()
		}, p.app.window)
}

// lastKeystoreReference reports whether removed is the only remaining wallet that
// references its keystore (i.e. removing it should wipe the keystore file). It must
// be called before the descriptor is removed from the config.
func (p *walletsPane) lastKeystoreReference(removed wallet.Descriptor) bool {
	if removed.KeystoreID == "" {
		return false
	}
	for _, w := range p.app.cfg.Wallets {
		if w.ID != removed.ID && w.KeystoreID == removed.KeystoreID {
			return false
		}
	}
	return true
}

// maybeWipeKeystore deletes a removed wallet's keystore file if no other wallet
// still references it. Called after the descriptor has been removed from config.
func (p *walletsPane) maybeWipeKeystore(removed wallet.Descriptor) {
	if removed.KeystoreID == "" {
		return
	}
	for _, w := range p.app.cfg.Wallets {
		if w.KeystoreID == removed.KeystoreID {
			return // still referenced by another account
		}
	}
	if ksDir, err := config.KeystoreDir(); err == nil {
		_ = keystore.Wipe(filepath.Join(ksDir, removed.KeystoreID+".json"))
	}
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
