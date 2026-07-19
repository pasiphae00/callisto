package ui

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/chain"
	"codeberg.org/pasiphae/callisto/internal/history"
	"codeberg.org/pasiphae/callisto/internal/signer"
	"codeberg.org/pasiphae/callisto/internal/tx"
	"codeberg.org/pasiphae/callisto/internal/walletconnect"
)

// wcApprovedMethods are the request methods Callisto advertises when settling a
// session (a broad, standard set so dApps work out of the box).
var wcApprovedMethods = []string{
	walletconnect.MethodSendTransaction,
	walletconnect.MethodSignTransaction,
	walletconnect.MethodPersonalSign,
	walletconnect.MethodSign,
	walletconnect.MethodSignTypedData,
	walletconnect.MethodSignTypedDataV4,
	walletconnect.MethodSwitchEthChain,
}

var wcApprovedEvents = []string{"chainChanged", "accountsChanged"}

// walletConnectPane connects Callisto to dApps as a wallet: paste a wc: URI,
// approve a session exposing the active wallet, and review + sign the dApp's
// requests. The heavy lifting (relay, crypto, session state) lives in
// internal/walletconnect; this pane is the UI + the bridge to Callisto's signer
// and transaction pipeline.
type walletConnectPane struct {
	app *App

	uriEntry    *widget.Entry
	status      *widget.Label
	sessionsBox *fyne.Container

	client *walletconnect.Client
}

func newWalletConnectPane(a *App) *walletConnectPane {
	return &walletConnectPane{app: a}
}

func (p *walletConnectPane) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord

	p.uriEntry = widget.NewEntry()
	p.uriEntry.SetPlaceHolder("wc:… (copy the WalletConnect link from a dApp)")
	connectBtn := widget.NewButton("Connect", p.onConnect)
	connectBtn.Importance = widget.HighImportance

	p.sessionsBox = container.NewVBox()

	header := widget.NewLabelWithStyle("WalletConnect", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("On a dApp (e.g. Uniswap) choose Connect, then WalletConnect, then Copy link. Paste it below and Connect, then Approve the session.\n\nThe dApp's requests then appear here to review and sign; the active wallet in the Wallets tab is the one used.")
	help.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(
		header, help,
		container.NewBorder(nil, nil, nil, connectBtn, p.uriEntry),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Active sessions", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)
	p.refreshSessions()
	return container.NewBorder(top, p.status, nil, nil, container.NewVScroll(p.sessionsBox))
}

// ensureClient lazily creates and connects the WalletConnect client, wiring the
// proposal/request callbacks to this pane.
func (p *walletConnectPane) ensureClient(ctx context.Context) (*walletconnect.Client, error) {
	if p.client != nil {
		return p.client, nil
	}
	c, err := walletconnect.NewClient(walletconnect.DefaultRelayURL, walletconnect.ProjectID(), walletconnect.WalletMetadata)
	if err != nil {
		return nil, err
	}
	c.OnProposal(func(prop walletconnect.Proposal) { fyne.Do(func() { p.showProposal(prop) }) })
	c.OnRequest(func(req walletconnect.Request) { fyne.Do(func() { p.showRequest(req) }) })
	c.OnSessionDelete(func(string) { fyne.Do(p.refreshSessions) })
	c.OnError(func(err error) { fyne.Do(func() { p.status.SetText("WalletConnect disconnected: " + err.Error()) }) })
	if err := c.Connect(ctx); err != nil {
		c.Close()
		return nil, err
	}
	p.client = c
	p.app.setWalletConnect(c)
	return c, nil
}

func (p *walletConnectPane) onConnect() {
	uri := strings.TrimSpace(p.uriEntry.Text)
	if uri == "" {
		dialog.ShowError(fmt.Errorf("paste a WalletConnect URI first"), p.app.window)
		return
	}
	if _, ok := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet); !ok {
		dialog.ShowError(fmt.Errorf("select a wallet in the Wallets tab first — it's the account you'll expose"), p.app.window)
		return
	}
	p.status.SetText("Connecting to the WalletConnect relay…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := p.ensureClient(ctx)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("relay connect: %w", err), p.app.window) })
			return
		}
		if err := c.Pair(ctx, uri); err != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("pair: %w", err), p.app.window) })
			return
		}
		fyne.Do(func() {
			p.uriEntry.SetText("")
			p.status.SetText("Paired — waiting for the dApp's session proposal…")
		})
	}()
}

// showProposal presents a session proposal for approval.
func (p *walletConnectPane) showProposal(prop walletconnect.Proposal) {
	desc, ok := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet)
	if !ok {
		dialog.ShowError(fmt.Errorf("no active wallet to expose; select one in Wallets"), p.app.window)
		return
	}
	chains := proposalChains(prop)
	if len(chains) == 0 {
		if conn, connected := p.app.rpc.Active(); connected {
			chains = []string{fmt.Sprintf("eip155:%d", conn.ChainID.Uint64())}
		}
	}

	rows := [][2]string{
		{"dApp", firstNonEmpty(prop.Proposer.Name, "(unknown)")},
		{"URL", prop.Proposer.URL},
		{"Expose account", desc.Label + " · " + address.Short(mustAddr(desc.Address))},
		{"Chains", strings.Join(chains, ", ")},
	}
	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		grid.Add(widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(r[1]))
	}
	note := widget.NewLabel("Approving lets this dApp request signatures and transactions from the exposed account. You review and confirm each one here.")
	note.Wrapping = fyne.TextWrapWord

	d := dialog.NewCustomConfirm("Session proposal", "Approve", "Reject",
		container.NewVBox(grid, widget.NewSeparator(), note),
		func(approve bool) {
			if approve {
				p.approve(prop, desc.Address, chains)
			} else {
				p.reject(prop)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(560, 380))
	d.Show()
}

func (p *walletConnectPane) approve(prop walletconnect.Proposal, account string, chains []string) {
	p.status.SetText("Approving session…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := p.client.Approve(ctx, prop.ID, address.Format(mustAddr(account)), chains, wcApprovedMethods, wcApprovedEvents)
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(fmt.Errorf("approve: %w", err), p.app.window)
				return
			}
			p.status.SetText("Session approved — " + prop.Proposer.Name)
			p.refreshSessions()
		})
	}()
}

func (p *walletConnectPane) reject(prop walletconnect.Proposal) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = p.client.Reject(ctx, prop.ID)
		fyne.Do(func() { p.status.SetText("Session rejected") })
	}()
}

// showRequest presents an inbound request and dispatches to signing on approval.
func (p *walletConnectPane) showRequest(req walletconnect.Request) {
	sess, _ := p.client.Session(req.SessionTopic)
	title, body, ok := p.describeRequest(req)
	if !ok {
		// Unsupported method — decline automatically with a clear reason.
		p.respondError(req, 4001, "Method not supported by Callisto: "+req.Method)
		p.status.SetText("Rejected unsupported request: " + req.Method)
		return
	}

	content := container.NewVBox(
		widget.NewLabelWithStyle("From "+firstNonEmpty(sess.Peer.Name, "dApp"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		body,
	)
	d := dialog.NewCustomConfirm(title, "Sign", "Reject", content, func(sign bool) {
		if !sign {
			p.respondError(req, 4001, "User rejected")
			return
		}
		p.dispatch(req, sess)
	}, p.app.window)
	d.Resize(fyne.NewSize(640, 520))
	d.Show()
}

// describeRequest builds a human-readable review for a request, or ok=false if the
// method is unsupported.
func (p *walletConnectPane) describeRequest(req walletconnect.Request) (title string, body fyne.CanvasObject, ok bool) {
	switch req.Method {
	case walletconnect.MethodSendTransaction, walletconnect.MethodSignTransaction:
		tp, err := walletconnect.DecodeTxParams(req.Params)
		if err != nil {
			return "", nil, false
		}
		rows := [][2]string{{"Method", req.Method}, {"Chain", req.ChainID}, {"From", address.Format(tp.From)}}
		if tp.To != nil {
			rows = append(rows, [2]string{"To", address.Format(*tp.To)})
		} else {
			rows = append(rows, [2]string{"To", "(contract creation)"})
		}
		rows = append(rows,
			[2]string{"Value", assets.FormatUnits(tp.Value, 18) + " ETH"},
			[2]string{"Data", shortHex(tp.Data)},
		)
		return "Transaction request", formGrid(rows), true

	case walletconnect.MethodPersonalSign, walletconnect.MethodSign:
		msg, addr, err := walletconnect.DecodePersonalSign(req.Params)
		if err != nil {
			return "", nil, false
		}
		rows := [][2]string{{"Method", req.Method}, {"Account", address.Format(addr)}}
		grid := formGrid(rows)
		msgBox := widget.NewMultiLineEntry()
		msgBox.SetText(personalMessageText(msg))
		msgBox.Wrapping = fyne.TextWrapWord
		msgBox.OnChanged = func(string) { msgBox.SetText(personalMessageText(msg)) } // read-only
		return "Signature request", container.NewBorder(grid, nil, nil, nil, msgBox), true

	case walletconnect.MethodSignTypedData, walletconnect.MethodSignTypedDataV4:
		addr, td, err := walletconnect.DecodeTypedData(req.Params)
		if err != nil {
			return "", nil, false
		}
		grid := formGrid([][2]string{{"Method", req.Method}, {"Account", address.Format(addr)}, {"Chain", req.ChainID}})
		tdBox := widget.NewMultiLineEntry()
		tdBox.SetText(string(td))
		tdBox.Wrapping = fyne.TextWrapWord
		tdBox.OnChanged = func(string) { tdBox.SetText(string(td)) } // read-only
		return "Typed-data signature request", container.NewBorder(grid, nil, nil, nil, tdBox), true

	default:
		return "", nil, false
	}
}

// dispatch executes an approved request with the active signer and answers the dApp.
func (p *walletConnectPane) dispatch(req walletconnect.Request, sess walletconnect.Session) {
	s, _, ok := p.app.currentSigner()
	if !ok {
		p.failRequest(req, "unlock the exposed wallet in the Wallets tab, then approve again")
		return
	}
	if sess.Account != "" && !addrEqual(s.Address(), sess.Account) {
		p.failRequest(req, "the unlocked wallet ("+address.Short(s.Address())+") is not the account this dApp session uses ("+sess.Account+")")
		return
	}
	p.status.SetText("Signing request from " + firstNonEmpty(sess.Peer.Name, "dApp") + "…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		switch req.Method {
		case walletconnect.MethodSendTransaction:
			p.dispatchSendTx(ctx, req, s)
		case walletconnect.MethodSignTransaction:
			p.dispatchSignTx(ctx, req, s)
		case walletconnect.MethodPersonalSign, walletconnect.MethodSign:
			p.dispatchPersonalSign(ctx, req, s)
		case walletconnect.MethodSignTypedData, walletconnect.MethodSignTypedDataV4:
			p.dispatchTypedData(ctx, req, s)
		default:
			p.respondError(req, 4001, "Method not supported")
		}
	}()
}

func (p *walletConnectPane) dispatchSendTx(ctx context.Context, req walletconnect.Request, s signer.Signer) {
	tp, err := walletconnect.DecodeTxParams(req.Params)
	if err != nil {
		p.failRequest(req, err.Error())
		return
	}
	if tp.To == nil {
		p.failRequest(req, "contract-creation transactions are not supported")
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.failRequest(req, "connect an RPC endpoint first")
		return
	}
	if want, okc := parseEIP155(req.ChainID); okc && want != conn.ChainID.Uint64() {
		p.failRequest(req, fmt.Sprintf("this request is for chain %d, but Callisto is connected to %d — switch RPC", want, conn.ChainID.Uint64()))
		return
	}

	send := tx.Send{From: s.Address(), Call: tx.Call{To: *tp.To, Value: tp.Value, Data: tp.Data}}
	prep, err := tx.Prepare(ctx, conn.Client, new(big.Int).Set(conn.ChainID), send)
	if err != nil {
		p.failRequest(req, "prepare: "+err.Error())
		return
	}
	signed, err := s.SignTx(ctx, prep.Tx, prep.ChainID)
	if err != nil {
		p.failRequest(req, "sign: "+err.Error())
		return
	}
	hash, err := tx.Broadcast(ctx, conn.Client, signed)
	if err != nil {
		p.failRequest(req, "broadcast: "+err.Error())
		return
	}
	p.recordHistory(conn.ChainID.Uint64(), s.Address(), *tp.To, tp.Value, hash.Hex(), req)
	p.respondResult(req, hash.Hex())
	info := conn.ChainInfo
	fyne.Do(func() {
		p.status.SetText("Transaction submitted to " + info.Name)
		p.showTxResult(hash.Hex(), info)
		if p.app.historyReload != nil {
			p.app.historyReload()
		}
	})
}

// showTxResult presents a submitted transaction with the hash in the monospace
// font and a clickable explorer link.
func (p *walletConnectPane) showTxResult(hash string, info chain.Info) {
	body := container.NewVBox(widget.NewLabel("Transaction submitted."), monoLabel(hash))
	if link := info.TxURL(hash); link != "" {
		body.Add(widget.NewButton("View on explorer", func() { p.app.openURL(link) }))
	}
	dialog.ShowCustom("WalletConnect transaction", "Close", body, p.app.window)
}

func (p *walletConnectPane) dispatchSignTx(ctx context.Context, req walletconnect.Request, s signer.Signer) {
	tp, err := walletconnect.DecodeTxParams(req.Params)
	if err != nil || tp.To == nil {
		p.failRequest(req, "unsupported transaction for signing")
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.failRequest(req, "connect an RPC endpoint first")
		return
	}
	send := tx.Send{From: s.Address(), Call: tx.Call{To: *tp.To, Value: tp.Value, Data: tp.Data}}
	prep, err := tx.Prepare(ctx, conn.Client, new(big.Int).Set(conn.ChainID), send)
	if err != nil {
		p.failRequest(req, "prepare: "+err.Error())
		return
	}
	signed, err := s.SignTx(ctx, prep.Tx, prep.ChainID)
	if err != nil {
		p.failRequest(req, "sign: "+err.Error())
		return
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		p.failRequest(req, "encode: "+err.Error())
		return
	}
	p.respondResult(req, "0x"+common.Bytes2Hex(raw))
	fyne.Do(func() { p.status.SetText("Signed transaction returned to dApp") })
}

func (p *walletConnectPane) dispatchPersonalSign(ctx context.Context, req walletconnect.Request, s signer.Signer) {
	ps, ok := s.(signer.PersonalSigner)
	if !ok {
		p.failRequest(req, "this wallet type cannot sign messages yet")
		return
	}
	msg, _, err := walletconnect.DecodePersonalSign(req.Params)
	if err != nil {
		p.failRequest(req, err.Error())
		return
	}
	sig, err := ps.SignPersonalMessage(ctx, msg)
	if err != nil {
		p.failRequest(req, "sign: "+err.Error())
		return
	}
	p.respondResult(req, "0x"+common.Bytes2Hex(sig))
	fyne.Do(func() { p.status.SetText("Message signed") })
}

func (p *walletConnectPane) dispatchTypedData(ctx context.Context, req walletconnect.Request, s signer.Signer) {
	ts, ok := s.(signer.TypedDataSigner)
	if !ok {
		p.failRequest(req, "this wallet type cannot sign typed data yet")
		return
	}
	_, td, err := walletconnect.DecodeTypedData(req.Params)
	if err != nil {
		p.failRequest(req, err.Error())
		return
	}
	sig, err := ts.SignTypedData(ctx, td)
	if err != nil {
		p.failRequest(req, "sign: "+err.Error())
		return
	}
	p.respondResult(req, "0x"+common.Bytes2Hex(sig))
	fyne.Do(func() { p.status.SetText("Typed data signed") })
}

// --- session list -----------------------------------------------------------

func (p *walletConnectPane) refreshSessions() {
	if p.sessionsBox == nil {
		return
	}
	p.sessionsBox.Objects = nil
	if p.client == nil {
		p.sessionsBox.Add(widget.NewLabel("Not connected."))
		p.sessionsBox.Refresh()
		return
	}
	sessions := p.client.Sessions()
	if len(sessions) == 0 {
		p.sessionsBox.Add(widget.NewLabel("No active sessions."))
	}
	for _, s := range sessions {
		s := s
		label := fmt.Sprintf("%s · %s · %s", firstNonEmpty(s.Peer.Name, "dApp"), address.Short(mustAddr(s.Account)), chainsSummary(s.Chains))
		disc := widget.NewButton("Disconnect", func() { p.disconnect(s.Topic) })
		p.sessionsBox.Add(container.NewBorder(nil, nil, nil, disc, widget.NewLabel(label)))
	}
	p.sessionsBox.Refresh()
}

func (p *walletConnectPane) disconnect(topic string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = p.client.Disconnect(ctx, topic)
		fyne.Do(p.refreshSessions)
	}()
}

// --- response helpers -------------------------------------------------------

func (p *walletConnectPane) respondResult(req walletconnect.Request, result interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = p.client.RespondResult(ctx, req, result)
}

func (p *walletConnectPane) respondError(req walletconnect.Request, code int, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = p.client.RespondError(ctx, req, code, message)
}

// failRequest answers the dApp with an error and surfaces it in the UI.
func (p *walletConnectPane) failRequest(req walletconnect.Request, msg string) {
	p.respondError(req, 4001, msg)
	fyne.Do(func() {
		p.status.SetText("Request failed: " + msg)
		dialog.ShowError(fmt.Errorf("%s", msg), p.app.window)
	})
}

func (p *walletConnectPane) recordHistory(chainID uint64, from, to common.Address, value *big.Int, hash string, req walletconnect.Request) {
	if p.app.history == nil {
		return
	}
	rec := history.Record{
		ChainID:       chainID,
		WalletAddress: address.Format(from),
		Kind:          "walletconnect",
		Instructions:  "WalletConnect " + req.Method,
		ToAddress:     address.Format(to),
		ValueWei:      value.String(),
		TxHash:        hash,
		Status:        history.StatusSubmitted,
	}
	if id, err := p.app.history.Insert(rec); err == nil {
		_ = p.app.history.MarkSubmitted(id, hash)
	}
}

// --- small helpers ----------------------------------------------------------

func proposalChains(prop walletconnect.Proposal) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ns map[string]walletconnect.Namespace) {
		if n, ok := ns["eip155"]; ok {
			for _, c := range n.Chains {
				if !seen[c] {
					seen[c] = true
					out = append(out, c)
				}
			}
		}
	}
	add(prop.RequiredNamespaces)
	add(prop.OptionalNamespaces)
	return out
}

// chainsSummary renders a session's chains compactly (dApps like Uniswap request
// dozens; showing them all made the window absurdly wide).
func chainsSummary(chains []string) string {
	switch len(chains) {
	case 0:
		return "no chains"
	case 1:
		return chainName(chains[0])
	default:
		return fmt.Sprintf("%s +%d more", chainName(chains[0]), len(chains)-1)
	}
}

// chainName maps a CAIP-2 chain id to a human name where known.
func chainName(caip2 string) string {
	if n, ok := parseEIP155(caip2); ok {
		if info, found := chain.Lookup(n); found {
			return info.Name
		}
		return fmt.Sprintf("chain %d", n)
	}
	return caip2
}

func parseEIP155(caip2 string) (uint64, bool) {
	parts := strings.SplitN(caip2, ":", 2)
	if len(parts) != 2 || parts[0] != "eip155" {
		return 0, false
	}
	n, err := strconv.ParseUint(parts[1], 10, 64)
	return n, err == nil
}

func formGrid(rows [][2]string) *fyne.Container {
	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		grid.Add(widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(r[1]))
	}
	return grid
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func mustAddr(s string) common.Address {
	a, _ := address.Parse(s)
	return a
}

func shortHex(b []byte) string {
	if len(b) == 0 {
		return "(none)"
	}
	h := "0x" + common.Bytes2Hex(b)
	if len(h) > 42 {
		return h[:42] + "…"
	}
	return h
}

// personalMessageText renders a personal-sign message as UTF-8 if printable, else
// as hex, for display.
func personalMessageText(msg []byte) string {
	if isPrintable(msg) {
		return string(msg)
	}
	return "0x" + common.Bytes2Hex(msg)
}

func isPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x09 || (c > 0x0d && c < 0x20) || c == 0x7f {
			return false
		}
	}
	return true
}
