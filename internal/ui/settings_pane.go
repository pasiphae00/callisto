package ui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// settingsPane manages the persisted RPC endpoint list: add, remove, select, and
// connect. Callisto ships no default endpoint, so this is the first thing a user
// configures. Connection state is reflected in the shared status bar.
type settingsPane struct {
	app *App

	list       *widget.List
	statusLbl  *widget.Label
	connectBtn *widget.Button
	removeBtn  *widget.Button

	selected int // index into app.cfg.Endpoints, or -1
}

func newSettingsPane(a *App) *settingsPane {
	return &settingsPane{app: a, selected: -1}
}

func (p *settingsPane) build() fyne.CanvasObject {
	p.statusLbl = widget.NewLabel("")
	p.statusLbl.Wrapping = fyne.TextWrapWord
	p.refreshStatus()

	p.list = widget.NewList(
		func() int { return len(p.app.cfg.Endpoints) },
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			e := p.app.cfg.Endpoints[i]
			label := e.Name + "  —  " + e.URL
			if e.AutoConnect {
				label += "   ⭐ default"
			}
			if p.app.cfg.ActiveEndpoint == e.Name {
				label = "● " + label
			}
			o.(*widget.Label).SetText(label)
		},
	)
	p.list.OnSelected = func(id widget.ListItemID) {
		p.selected = id
		p.updateButtons()
	}
	p.list.OnUnselected = func(widget.ListItemID) {
		p.selected = -1
		p.updateButtons()
	}

	addBtn := widget.NewButton("Add endpoint…", p.showAddDialog)
	p.connectBtn = widget.NewButton("Connect", p.connectSelected)
	p.removeBtn = widget.NewButton("Remove", p.removeSelected)
	p.updateButtons()

	buttons := container.NewHBox(addBtn, p.connectBtn, p.removeBtn)
	header := widget.NewLabelWithStyle("RPC endpoints", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("Callisto has no default RPC. Add your own node (http(s) or ws(s)); ws(s) enables live updates.")
	help.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(header, help, buttons, p.statusLbl, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, p.list)
}

func (p *settingsPane) updateButtons() {
	has := p.selected >= 0 && p.selected < len(p.app.cfg.Endpoints)
	if p.connectBtn != nil {
		if has {
			p.connectBtn.Enable()
			p.removeBtn.Enable()
		} else {
			p.connectBtn.Disable()
			p.removeBtn.Disable()
		}
	}
}

func (p *settingsPane) refreshStatus() {
	if conn, ok := p.app.rpc.Active(); ok {
		name := conn.ChainInfo.Name
		if !conn.Known {
			name = fmt.Sprintf("unknown chain %d", conn.ChainID)
		}
		p.statusLbl.SetText(fmt.Sprintf("Connected to %s — %s (%s)", conn.Endpoint.Name, name, conn.Endpoint.Scheme()))
	} else {
		p.statusLbl.SetText("Not connected.")
	}
}

// showAddDialog collects a name + URL, validates, persists, and refreshes.
func (p *settingsPane) showAddDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("e.g. Sepolia (Infura)")
	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("https://… or wss://…")
	defaultCheck := widget.NewCheck("Auto-connect on startup (default endpoint)", nil)

	items := []*widget.FormItem{
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("URL", urlEntry),
		widget.NewFormItem("", defaultCheck),
	}
	d := dialog.NewForm("Add RPC endpoint", "Add", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		e := rpc.Endpoint{Name: nameEntry.Text, URL: urlEntry.Text}
		if err := p.app.cfg.UpsertEndpoint(e); err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		if defaultCheck.Checked {
			// Exclusive default: this endpoint auto-connects, others don't.
			p.app.cfg.SetAutoConnect(e.Name)
		}
		if err := p.app.cfg.Save(); err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		p.list.Refresh()
	}, p.app.window)
	d.Resize(fyne.NewSize(480, 240))
	d.Show()
}

// connectSelected dials the selected endpoint in the background and, on success,
// marks it active and persists the (possibly chain-ID-annotated) endpoint.
func (p *settingsPane) connectSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Endpoints) {
		return
	}
	e := p.app.cfg.Endpoints[p.selected]
	p.connectBtn.Disable()
	p.statusLbl.SetText("Connecting to " + e.Name + "…")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := p.app.rpc.Connect(ctx, e)
		fyne.Do(func() {
			p.connectBtn.Enable()
			if err != nil {
				dialog.ShowError(err, p.app.window)
				p.refreshStatus()
				return
			}
			// Persist the observed chain ID and mark active.
			e.ChainID = conn.ChainID.Uint64()
			_ = p.app.cfg.UpsertEndpoint(e)
			p.app.cfg.ActiveEndpoint = e.Name
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
			}
			p.list.Refresh()
			p.refreshStatus()
			p.app.refreshStatusBar()
		})
	}()
}

func (p *settingsPane) removeSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Endpoints) {
		return
	}
	e := p.app.cfg.Endpoints[p.selected]
	dialog.ShowConfirm("Remove endpoint", "Remove \""+e.Name+"\"?", func(ok bool) {
		if !ok {
			return
		}
		wasActive := p.app.cfg.ActiveEndpoint == e.Name
		p.app.cfg.RemoveEndpoint(e.Name)
		if wasActive {
			p.app.rpc.Disconnect()
		}
		if err := p.app.cfg.Save(); err != nil {
			dialog.ShowError(err, p.app.window)
		}
		p.selected = -1
		p.list.UnselectAll()
		p.list.Refresh()
		p.updateButtons()
		p.refreshStatus()
		p.app.refreshStatusBar()
	}, p.app.window)
}
