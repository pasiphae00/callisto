package ui

import (
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/config"
	"codeberg.org/pasiphae/callisto/internal/keystore"
	"codeberg.org/pasiphae/callisto/internal/signer/hot"
	"codeberg.org/pasiphae/callisto/internal/wallet"
)

// touchIDRef is the OS-keychain reference for a keystore's cached unlock key.
func touchIDRef(keystoreID string) string { return "keystore-" + keystoreID }

// enableTouchIDSelected enrolls the selected wallet for Touch ID unlock: it verifies
// the passphrase, then stores the keystore's derived AES key in the OS keychain,
// gated by Touch ID.
func (p *walletsPane) enableTouchIDSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Wallets) {
		return
	}
	desc := p.app.cfg.Wallets[p.selected]
	if desc.IsHardware() || desc.KeystoreID == "" {
		dialog.ShowError(fmt.Errorf("only hot wallets can use Touch ID"), p.app.window)
		return
	}
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder("wallet passphrase")
	content := container.NewVBox(
		widget.NewLabel("Confirm your passphrase to enable Touch ID unlock. Your passphrase still works as a fallback, and the recovery phrase is never stored."),
		widget.NewForm(widget.NewFormItem("Passphrase", pass)),
	)
	d := dialog.NewCustomConfirm("Enable Touch ID — "+displayName(desc), "Enable", "Cancel", content,
		func(ok bool) {
			if ok {
				p.doEnableTouchID(desc, pass.Text)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(480, 260))
	d.Show()
}

func (p *walletsPane) doEnableTouchID(desc wallet.Descriptor, pass string) {
	ksDir, err := config.KeystoreDir()
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	ks, err := keystore.Load(filepath.Join(ksDir, desc.KeystoreID+".json"))
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	progress := dialog.NewCustomWithoutButtons("Enrolling…", widget.NewLabel("Verifying and storing in the keychain…"), p.app.window)
	progress.Show()
	go func() {
		// Derive the key and verify the passphrase is correct before storing it.
		key, derr := ks.DeriveKey(pass)
		if derr == nil {
			if _, verr := ks.DecryptWithKey(key); verr != nil {
				derr = verr
			}
		}
		var storeErr error
		if derr == nil {
			storeErr = keystore.OSSecretStore().Set(touchIDRef(desc.KeystoreID), key)
		}
		fyne.Do(func() {
			progress.Hide()
			if derr != nil {
				dialog.ShowError(fmt.Errorf("wrong passphrase"), p.app.window)
				return
			}
			if storeErr != nil {
				dialog.ShowError(fmt.Errorf("could not store the key in the keychain: %w\n\n(Touch ID may require a code-signed build.)", storeErr), p.app.window)
				return
			}
			p.app.cfg.SetTouchIDEnrolled(desc.KeystoreID, true)
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			dialog.ShowInformation("Touch ID enabled", "You can now unlock "+displayName(desc)+" with Touch ID. Your passphrase still works too.", p.app.window)
		})
	}()
}

// disableTouchIDSelected removes the keychain item and un-enrolls the wallet.
func (p *walletsPane) disableTouchIDSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Wallets) {
		return
	}
	desc := p.app.cfg.Wallets[p.selected]
	_ = keystore.OSSecretStore().Delete(touchIDRef(desc.KeystoreID))
	p.app.cfg.SetTouchIDEnrolled(desc.KeystoreID, false)
	if err := p.app.cfg.Save(); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	dialog.ShowInformation("Touch ID disabled", displayName(desc)+" now unlocks with its passphrase only.", p.app.window)
}

// unlockWithTouchID unlocks a Touch-ID-enrolled wallet by fetching its key from the
// keychain (which prompts for Touch ID) and installing the signer.
func (p *walletsPane) unlockWithTouchID(desc wallet.Descriptor) {
	progress := dialog.NewCustomWithoutButtons("Touch ID", widget.NewLabel("Confirm with Touch ID…"), p.app.window)
	progress.Show()
	go func() {
		key, err := keystore.OSSecretStore().Get(touchIDRef(desc.KeystoreID)) // prompts Touch ID
		var w *hot.Wallet
		if err == nil {
			var ksDir string
			ksDir, err = config.KeystoreDir()
			if err == nil {
				var ks *keystore.Keystore
				if ks, err = keystore.Load(filepath.Join(ksDir, desc.KeystoreID+".json")); err == nil {
					path := desc.DerivationPath
					if path == "" {
						path = hot.DefaultPath(0)
					}
					w, err = hot.OpenFromKeystoreWithKey(ks, key, path)
				}
			}
			for i := range key {
				key[i] = 0
			}
		}
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(fmt.Errorf("Touch ID unlock failed: %w\n\nYou can unlock with your passphrase instead.", err), p.app.window)
				return
			}
			if !addrEqual(w.Address(), desc.Address) {
				w.Lock()
				dialog.ShowError(fmt.Errorf("the stored key derived a different address than this wallet"), p.app.window)
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
