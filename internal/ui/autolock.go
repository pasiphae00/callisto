package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

// autoLockInterval is how often the watcher checks for idleness / wake-from-sleep.
const autoLockInterval = 30 * time.Second

// touchActivity records user activity, resetting the idle auto-lock timer.
func (a *App) touchActivity() {
	a.secMu.Lock()
	a.lastActive = time.Now()
	a.secMu.Unlock()
}

func (a *App) idleSince() time.Duration {
	a.secMu.Lock()
	defer a.secMu.Unlock()
	return time.Since(a.lastActive)
}

// startAutoLock runs the background watcher that locks the active signer after the
// configured idle period, and on wake-from-sleep. It is a no-op unless a wallet is
// unlocked and a policy is enabled, so it's cheap to always run.
func (a *App) startAutoLock() {
	a.touchActivity()
	go func() {
		ticker := time.NewTicker(autoLockInterval)
		defer ticker.Stop()
		last := time.Now()
		for range ticker.C {
			now := time.Now()
			gap := now.Sub(last)
			last = now

			// Nothing to protect unless a wallet is unlocked.
			if _, _, ok := a.currentSigner(); !ok {
				continue
			}
			// Wake-from-sleep: the process is suspended while the Mac sleeps, so a
			// wall-clock gap much larger than the tick interval means it just woke.
			if a.cfg.Security.LockOnSleep && gap > 3*autoLockInterval {
				a.lockForSecurity("Wallet locked after the computer woke from sleep.")
				continue
			}
			if mins := a.cfg.Security.AutoLockMinutes; mins > 0 && a.idleSince() >= time.Duration(mins)*time.Minute {
				a.lockForSecurity(fmt.Sprintf("Wallet locked after %d minutes of inactivity.", mins))
			}
		}
	}()
}

// lockForSecurity wipes the active signer and tells the user why, on the UI thread.
func (a *App) lockForSecurity(reason string) {
	fyne.Do(func() {
		if _, _, ok := a.currentSigner(); !ok {
			return
		}
		a.clearSigner()
		a.refreshStatusBar()
		if a.window != nil {
			dialog.ShowInformation("Wallet locked", reason+"\n\nUnlock it again in the Wallets tab when you need to sign.", a.window)
		}
	})
}
