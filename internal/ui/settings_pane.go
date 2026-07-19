package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// endpointRow is a settings-list row: a single tap selects it (driven explicitly,
// because a custom tappable widget consumes the tap the List would otherwise use
// for selection), and a double tap opens the editor.
type endpointRow struct {
	widget.BaseWidget
	dot         *canvas.Text
	name        *widget.Label
	url         *widget.Label
	def         *widget.Label
	onTap       func()
	onDoubleTap func()
}

func newEndpointRow() *endpointRow {
	r := &endpointRow{
		dot:  canvas.NewText("●", statusGray),
		name: widget.NewLabel("name"),
		url:  monoLabel("url"),
		def:  widget.NewLabel(""),
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *endpointRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewHBox(r.dot, r.name, r.url, r.def))
}

// Tapped selects this row.
func (r *endpointRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

// DoubleTapped opens the edit dialog for this endpoint.
func (r *endpointRow) DoubleTapped(*fyne.PointEvent) {
	if r.onDoubleTap != nil {
		r.onDoubleTap()
	}
}

const rpcHelpText = `Callisto uses a "Flashbots Protect" Ethereum Mainnet RPC by default. Replace or add your own here (http/s or ws/s). To customize Flashbots Protect behaviour, generate a new endpoint at "protect.flashbots.net" (e.g. to set a MEV refund address, configure block builders, etc.).

Select "Auto-connect" when adding the endpoint, Callisto will use that RPC automatically on each subsequent restart.`

// settingsPane manages the persisted RPC endpoint list: add, remove, select, and
// connect. Callisto ships no default endpoint, so this is the first thing a user
// configures. Connection state is reflected in the shared status bar.
type settingsPane struct {
	app *App

	list       *widget.List
	statusLbl  *widget.Label
	connectBtn *widget.Button
	removeBtn  *widget.Button
	defaultBtn *widget.Button

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
		func() fyne.CanvasObject { return newEndpointRow() },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			e := p.app.cfg.Endpoints[i]
			row := o.(*endpointRow)

			if p.app.cfg.ActiveEndpoint == e.Name {
				row.dot.Color = statusGreen
			} else {
				row.dot.Color = colorTransparent
			}
			row.dot.Refresh()
			row.name.SetText(e.Name + "  —")
			row.url.SetText(e.URL)
			if e.AutoConnect {
				row.def.SetText("  ⭐ default")
			} else {
				row.def.SetText("")
			}
			row.onTap = func() { p.list.Select(i) }        // single-click selects
			row.onDoubleTap = func() { p.showEditDialog(i) } // double-click edits
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
	p.defaultBtn = widget.NewButton("Set Default", p.setDefaultSelected)
	p.removeBtn = widget.NewButton("Remove", p.removeSelected)
	p.updateButtons()

	buttons := container.NewHBox(addBtn, p.connectBtn, p.defaultBtn, p.removeBtn)
	header := widget.NewLabelWithStyle("RPC endpoints", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel(rpcHelpText)
	help.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(header, help, indentToText(buttons), p.statusLbl, widget.NewSeparator())
	bottom := container.NewVBox(p.buildSecurityBox(), p.buildUpdatesBox())
	return container.NewBorder(top, bottom, nil, nil, p.list)
}

func (p *settingsPane) updateButtons() {
	has := p.selected >= 0 && p.selected < len(p.app.cfg.Endpoints)
	if p.connectBtn == nil {
		return
	}
	for _, b := range []*widget.Button{p.connectBtn, p.removeBtn, p.defaultBtn} {
		if has {
			b.Enable()
		} else {
			b.Disable()
		}
	}
}

// setDefaultSelected makes the selected endpoint the exclusive auto-connect
// default (the one Callisto connects to on startup).
func (p *settingsPane) setDefaultSelected() {
	if p.selected < 0 || p.selected >= len(p.app.cfg.Endpoints) {
		return
	}
	name := p.app.cfg.Endpoints[p.selected].Name
	p.app.cfg.SetAutoConnect(name)
	if err := p.app.cfg.Save(); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	p.list.Refresh()
}

// showEditDialog opens an editor for the endpoint at idx: change its label and
// URL, plus Set Default / Remove shortcuts (Remove in a danger-red shade).
func (p *settingsPane) showEditDialog(idx int) {
	if idx < 0 || idx >= len(p.app.cfg.Endpoints) {
		return
	}
	e := p.app.cfg.Endpoints[idx]
	oldName := e.Name

	label := widget.NewEntry()
	label.SetText(e.Name)
	urlEntry := widget.NewEntry()
	urlEntry.SetText(e.URL)

	var d dialog.Dialog
	setDefaultBtn := widget.NewButton("Set Default", func() {
		p.app.cfg.SetAutoConnect(oldName)
		if err := p.app.cfg.Save(); err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		p.list.Refresh()
		d.Hide()
	})
	removeBtn := widget.NewButton("Remove", func() {
		p.app.cfg.RemoveEndpoint(oldName)
		if err := p.app.cfg.Save(); err != nil {
			dialog.ShowError(err, p.app.window)
		}
		p.selected = -1
		p.list.UnselectAll()
		p.updateButtons()
		p.list.Refresh()
		p.app.refreshStatusBar()
		d.Hide()
	})
	removeBtn.Importance = widget.DangerImportance

	form := widget.NewForm(
		widget.NewFormItem("Label", label),
		widget.NewFormItem("URL", urlEntry),
	)
	content := container.NewVBox(form, widget.NewSeparator(), container.NewHBox(setDefaultBtn, removeBtn))

	d = dialog.NewCustomConfirm("Edit endpoint", "Save", "Cancel", content, func(save bool) {
		if save {
			p.saveEdit(oldName, label.Text, urlEntry.Text, e.AutoConnect)
		}
	}, p.app.window)
	d.Resize(fyne.NewSize(600, 320))
	d.Show()
}

// saveEdit applies label/URL changes to an endpoint. Endpoints are keyed by label
// (Name), so a label change is a rename (remove old + add new), preserving the
// active selection and default flag.
func (p *settingsPane) saveEdit(oldName, newLabel, newURL string, wasDefault bool) {
	newLabel = strings.TrimSpace(newLabel)
	e := rpc.Endpoint{Name: newLabel, URL: strings.TrimSpace(newURL), AutoConnect: wasDefault}
	if err := e.Validate(); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	if newLabel != oldName {
		wasActive := p.app.cfg.ActiveEndpoint == oldName
		p.app.cfg.RemoveEndpoint(oldName)
		if wasActive {
			p.app.cfg.ActiveEndpoint = newLabel
		}
	}
	if err := p.app.cfg.UpsertEndpoint(e); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	if wasDefault {
		p.app.cfg.SetAutoConnect(newLabel)
	}
	if err := p.app.cfg.Save(); err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	p.list.Refresh()
	p.app.refreshStatusBar()
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
