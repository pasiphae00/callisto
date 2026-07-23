package ui

import (
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/pasiphae00/callisto/internal/address"
	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/keystore"
	"github.com/pasiphae00/callisto/internal/signer/hot"
	"github.com/pasiphae00/callisto/internal/wallet"
)

// showAddMenu is the "Add wallet…" sheet: seed, hardware, or an imported key /
// keystore file / watch-only address.
func (p *walletsPane) showAddMenu() {
	box := container.NewVBox()
	d := dialog.NewCustom("Add wallet", "Close", box, p.app.window)
	item := func(label string, fn func()) {
		b := widget.NewButton(label, func() { d.Hide(); fn() })
		b.Alignment = widget.ButtonAlignLeading
		box.Add(b)
	}
	item("Hot wallet (recovery phrase)…", p.showAddHotWallet)
	item("Hardware wallet…", p.showAddHardwareWallet)
	item("Import private key…", p.showImportPrivateKey)
	item("Import keystore file…", p.showImportKeystoreFile)
	item("Watch address (view-only)…", p.showAddWatchOnly)
	d.Resize(fyne.NewSize(360, 300))
	d.Show()
}

// --- import raw private key -------------------------------------------------

func (p *walletsPane) showImportPrivateKey() {
	label := widget.NewEntry()
	label.SetPlaceHolder("e.g. Imported")
	pk := widget.NewPasswordEntry()
	pk.SetPlaceHolder("0x… 64-hex-character private key")
	ksPass := widget.NewPasswordEntry()
	ksPass.SetPlaceHolder("passphrase to encrypt it")
	ksPass2 := widget.NewPasswordEntry()
	ksPass2.SetPlaceHolder("confirm passphrase")
	hint := widget.NewLabel("")
	ksPass.OnChanged = func(s string) { hint.SetText(passphraseStrength(s)) }

	form := container.NewVBox(
		cautionBox("Only import a private key you already trust and control — whoever holds it controls the account. Callisto will encrypt it with the passphrase you set below."),
		widget.NewLabelWithStyle("Label", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), label,
		widget.NewLabelWithStyle("Private key", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), pk,
		widget.NewLabelWithStyle("Encryption passphrase", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), ksPass, ksPass2, hint,
	)
	d := dialog.NewCustomConfirm("Import private key", "Import", "Cancel", container.NewVScroll(form),
		func(ok bool) {
			defer pk.SetText("")
			if ok {
				p.doImportPrivateKey(label.Text, pk.Text, ksPass.Text, ksPass2.Text)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(560, 560))
	d.Show()
}

func (p *walletsPane) doImportPrivateKey(label, pkHex, pass, pass2 string) {
	if len(pass) < minKeystorePassphraseLen {
		dialog.ShowError(fmt.Errorf("choose an encryption passphrase of at least %d characters", minKeystorePassphraseLen), p.app.window)
		return
	}
	if pass != pass2 {
		dialog.ShowError(fmt.Errorf("the passphrases do not match"), p.app.window)
		return
	}
	progress := dialog.NewCustomWithoutButtons("Encrypting…", widget.NewLabel("Sealing the key…"), p.app.window)
	progress.Show()
	go func() {
		ks, addr, err := hot.NewPrivateKeyKeystore(pkHex, pass)
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			p.finishImport(label, "Imported", ks, addr)
		})
	}()
}

// --- import geth/MetaMask V3 keystore file ----------------------------------

func (p *walletsPane) showImportKeystoreFile() {
	got := func(data []byte) { p.promptKeystoreFilePassword(data) }
	if nativeDialogsAvailable() {
		go func() {
			path, err := nativeOpenPath("Choose an Ethereum keystore JSON file")
			if err != nil || path == "" {
				return
			}
			data, rerr := os.ReadFile(path)
			fyne.Do(func() {
				if rerr != nil {
					dialog.ShowError(rerr, p.app.window)
					return
				}
				got(data)
			})
		}()
		return
	}
	open := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil || rc == nil {
			return
		}
		path := rc.URI().Path()
		_ = rc.Close()
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			dialog.ShowError(rerr, p.app.window)
			return
		}
		got(data)
	}, p.app.window)
	open.Show()
}

func (p *walletsPane) promptKeystoreFilePassword(data []byte) {
	label := widget.NewEntry()
	label.SetPlaceHolder("e.g. Imported")
	filePass := widget.NewPasswordEntry()
	filePass.SetPlaceHolder("password for the keystore file")
	ksPass := widget.NewPasswordEntry()
	ksPass.SetPlaceHolder("new Callisto passphrase")
	ksPass2 := widget.NewPasswordEntry()
	ksPass2.SetPlaceHolder("confirm Callisto passphrase")
	hint := widget.NewLabel("")
	ksPass.OnChanged = func(s string) { hint.SetText(passphraseStrength(s)) }

	form := container.NewVBox(
		cautionBox("Import a keystore file only from a source you trust. Callisto decrypts it with the file password, then re-encrypts the key under your new Callisto passphrase."),
		widget.NewLabelWithStyle("Label", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), label,
		widget.NewLabelWithStyle("Keystore file password", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), filePass,
		widget.NewLabelWithStyle("New Callisto passphrase (used to unlock in Callisto)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), ksPass, ksPass2, hint,
	)
	d := dialog.NewCustomConfirm("Import keystore file", "Import", "Cancel", container.NewVScroll(form),
		func(ok bool) {
			if ok {
				p.doImportKeystoreFile(label.Text, data, filePass.Text, ksPass.Text, ksPass2.Text)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(560, 580))
	d.Show()
}

func (p *walletsPane) doImportKeystoreFile(label string, data []byte, filePass, pass, pass2 string) {
	if len(pass) < minKeystorePassphraseLen {
		dialog.ShowError(fmt.Errorf("choose a Callisto passphrase of at least %d characters", minKeystorePassphraseLen), p.app.window)
		return
	}
	if pass != pass2 {
		dialog.ShowError(fmt.Errorf("the Callisto passphrases do not match"), p.app.window)
		return
	}
	progress := dialog.NewCustomWithoutButtons("Importing…", widget.NewLabel("Decrypting the keystore file…"), p.app.window)
	progress.Show()
	go func() {
		key, derr := gethkeystore.DecryptKey(data, filePass)
		var (
			ks   *keystore.Keystore
			addr common.Address
			err  error
		)
		if derr != nil {
			err = fmt.Errorf("could not decrypt the keystore file (wrong password or unsupported format)")
		} else {
			privHex := common.Bytes2Hex(crypto.FromECDSA(key.PrivateKey))
			ks, addr, err = hot.NewPrivateKeyKeystore(privHex, pass)
		}
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			p.finishImport(label, "Imported", ks, addr)
		})
	}()
}

// finishImport persists a single-key import: saves its keystore, adds an active
// descriptor, and refreshes.
func (p *walletsPane) finishImport(label, fallbackLabel string, ks *keystore.Keystore, addr common.Address) {
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
	if label == "" {
		label = fallbackLabel
	}
	desc := wallet.Descriptor{
		ID:         newWalletID(),
		Label:      label,
		Address:    address.Format(addr),
		Kind:       wallet.KindHot,
		KeystoreID: ksID,
	}
	if err := p.app.cfg.UpsertWallet(desc); err != nil {
		_ = keystore.Wipe(filepath.Join(ksDir, ksID+".json"))
		dialog.ShowError(err, p.app.window)
		return
	}
	p.app.cfg.ActiveWallet = desc.ID
	if err := p.app.cfg.Save(); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	p.refresh()
	dialog.ShowInformation("Wallet imported", desc.Label+"\n"+desc.Address, p.app.window)
}

// --- watch-only -------------------------------------------------------------

func (p *walletsPane) showAddWatchOnly() {
	label := widget.NewEntry()
	label.SetPlaceHolder("e.g. Vault")
	addr := widget.NewEntry()
	addr.SetPlaceHolder("0x… address")
	d := dialog.NewForm("Watch address", "Add", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Label", label),
			widget.NewFormItem("Address", addr),
		},
		func(ok bool) {
			if !ok {
				return
			}
			parsed, err := address.Parse(addr.Text)
			if err != nil {
				dialog.ShowError(fmt.Errorf("invalid address: %w", err), p.app.window)
				return
			}
			name := label.Text
			if name == "" {
				name = "Watch " + address.Short(parsed)
			}
			desc := wallet.Descriptor{
				ID:      newWalletID(),
				Label:   name,
				Address: address.Format(parsed),
				Kind:    wallet.KindWatch,
			}
			if err := p.app.cfg.UpsertWallet(desc); err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			p.refresh()
			dialog.ShowInformation("Watch-only added", desc.Label+"\n"+desc.Address+"\n\nView-only: it can't sign.", p.app.window)
		}, p.app.window)
	d.Resize(fyne.NewSize(500, 220))
	d.Show()
}
