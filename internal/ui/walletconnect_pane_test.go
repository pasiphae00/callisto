package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/walletconnect"
)

func TestWalletConnectPaneBuilds(t *testing.T) {
	test.NewApp()
	p := newWalletConnectPane(New(&config.Config{}, nil))
	root := p.build()
	if root == nil {
		t.Fatal("walletconnect pane build returned nil")
	}
	// No client yet → the sessions box shows the not-connected state.
	if len(p.sessionsBox.Objects) == 0 {
		t.Error("sessions box should render a placeholder before connecting")
	}
	w := test.NewWindow(root)
	defer w.Close()
}

func TestParseEIP155(t *testing.T) {
	if n, ok := parseEIP155("eip155:137"); !ok || n != 137 {
		t.Errorf("parseEIP155(eip155:137) = %d, %v", n, ok)
	}
	if _, ok := parseEIP155("cosmos:1"); ok {
		t.Error("non-eip155 namespace should not parse")
	}
	if _, ok := parseEIP155("eip155:notanumber"); ok {
		t.Error("non-numeric chain should not parse")
	}
}

func TestProposalChainsDedup(t *testing.T) {
	prop := walletconnect.Proposal{
		RequiredNamespaces: map[string]walletconnect.Namespace{"eip155": {Chains: []string{"eip155:1"}}},
		OptionalNamespaces: map[string]walletconnect.Namespace{"eip155": {Chains: []string{"eip155:1", "eip155:137"}}},
	}
	got := proposalChains(prop)
	if len(got) != 2 || got[0] != "eip155:1" || got[1] != "eip155:137" {
		t.Errorf("proposalChains = %v, want [eip155:1 eip155:137]", got)
	}
}

func TestPersonalMessageText(t *testing.T) {
	if s := personalMessageText([]byte("Hello dApp")); s != "Hello dApp" {
		t.Errorf("printable message = %q", s)
	}
	if s := personalMessageText([]byte{0x00, 0x01, 0xff}); s != "0x0001ff" {
		t.Errorf("binary message = %q, want hex", s)
	}
}
