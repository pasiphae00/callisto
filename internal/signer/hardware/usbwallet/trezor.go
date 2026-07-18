// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Copied verbatim (unmodified) from go-ethereum@v1.17.4 accounts/usbwallet as
// part of a local fork; see hub.go's package doc comment for why and what was
// changed elsewhere in this package.
//
// This file contains the implementation for interacting with the Trezor hardware
// wallets. The wire protocol spec can be found on the SatoshiLabs website:
// https://doc.satoshilabs.com/trezor-tech/api-protobuf.html

package usbwallet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/usbwallet/trezor"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// ErrTrezorPINNeeded is returned if opening the trezor requires a PIN code. In
// this case, the calling application should display a pinpad and send back the
// encoded passphrase.
var ErrTrezorPINNeeded = errors.New("trezor: pin needed")

// ErrTrezorPassphraseNeeded is returned if opening the trezor requires a passphrase
var ErrTrezorPassphraseNeeded = errors.New("trezor: passphrase needed")

// errTrezorReplyInvalidHeader is the error message returned by a Trezor data exchange
// if the device replies with a mismatching header. This usually means the device
// is in browser mode.
var errTrezorReplyInvalidHeader = errors.New("trezor: invalid reply header")

// trezorTransport abstracts how trezorDriver exchanges raw wire messages with a
// device: either chunked USB HID reports (usbTrezorTransport, upstream's
// original mechanism) or Trezor Bridge's HTTP API (bridgeTrezorTransport,
// Callisto-local addition — see hub.go's doc comment and trezor_bridge.go).
// Given a message type and marshaled protobuf payload, it returns the raw
// response's message type and payload.
type trezorTransport interface {
	exchange(msgType uint16, data []byte) (replyType uint16, reply []byte, err error)
}

// trezorDriver implements the communication with a Trezor hardware wallet.
type trezorDriver struct {
	transport      trezorTransport // Wire transport to the device (USB or Bridge)
	version        [3]uint32       // Current version of the Trezor firmware
	label          string          // Current textual label of the Trezor device
	pinwait        bool            // Flags whether the device is waiting for PIN entry
	passphrasewait bool            // Flags whether the device is waiting for passphrase entry
	initialized    bool            // Callisto-local: whether Initialize+ClearSession+Ping has run this Open
	passphrase     string          // Last passphrase supplied via Open/OpenBridge; "" selects the standard (non-hidden) wallet
	failure        error           // Any failure that would make the device unusable
	log            log.Logger      // Contextual logger to tag the trezor with its id
}

// newTrezorDriver creates a new instance of a Trezor USB protocol driver.
func newTrezorDriver(logger log.Logger) driver {
	return &trezorDriver{
		log: logger,
	}
}

// Status implements accounts.Wallet, always whether the Trezor is opened, closed
// or whether the Ethereum app was not started on it.
func (w *trezorDriver) Status() (string, error) {
	if w.failure != nil {
		return fmt.Sprintf("Failed: %v", w.failure), w.failure
	}
	if w.transport == nil {
		return "Closed", w.failure
	}
	if w.pinwait {
		return fmt.Sprintf("Trezor v%d.%d.%d '%s' waiting for PIN", w.version[0], w.version[1], w.version[2], w.label), w.failure
	}
	return fmt.Sprintf("Trezor v%d.%d.%d '%s' online", w.version[0], w.version[1], w.version[2], w.label), w.failure
}

// Open implements usbwallet.driver, attempting to initialize the connection to
// the Trezor hardware wallet. Initializing the Trezor is a two or three phase operation:
//   - The first phase is to initialize the connection and read the wallet's
//     features. This phase is invoked if the provided passphrase is empty. The
//     device will display the pinpad as a result and will return an appropriate
//     error to notify the user that a second open phase is needed.
//   - The second phase is to unlock access to the Trezor, which is done by the
//     user actually providing a passphrase mapping a keyboard keypad to the pin
//     number of the user (shuffled according to the pinpad displayed).
//   - If needed the device will ask for passphrase which will require calling
//     open again with the actual passphrase (3rd phase)
func (w *trezorDriver) Open(device io.ReadWriter, passphrase string) error {
	w.transport, w.failure = &usbTrezorTransport{device: device, log: w.log}, nil
	w.passphrase = passphrase
	return w.openProtocol(passphrase)
}

// OpenBridge is like Open but for a device reached via Trezor Bridge instead of
// direct USB access. Callisto-local addition; see trezor_bridge.go.
func (w *trezorDriver) OpenBridge(transport trezorTransport, passphrase string) error {
	w.transport, w.failure = transport, nil
	w.passphrase = passphrase
	return w.openProtocol(passphrase)
}

// openProtocol runs the Trezor Open handshake (Initialize/ClearSession/Ping/
// PIN/passphrase) over whichever transport was just installed in w.transport.
// Split out from Open/OpenBridge so both entry points share this
// transport-agnostic logic.
//
// Callisto-local change from upstream: the original gate here was
// `passphrase == "" && !w.passphrasewait`, matching upstream's multi-round-trip
// calling convention (call with "" first to learn whether PIN/passphrase are
// needed, then call again with the real value). Callisto always calls
// Open/OpenBridge once with the final passphrase already known (from the "Add
// hardware wallet" dialog), so that gate silently skipped Initialize —
// and therefore ClearSession — whenever a non-empty passphrase was supplied on
// a fresh driver, confirmed live: it caused a device with a cached unlock from
// a *previous* Open (e.g. a prior attempt with a different passphrase) to
// answer with the stale cached derivation instead of the one just requested.
// Initialize/ClearSession/Ping now always run once per fresh Open, regardless
// of whether a passphrase was supplied up front.
func (w *trezorDriver) openProtocol(passphrase string) error {
	if !w.initialized {
		// If we're already waiting for a PIN entry, insta-return
		if w.pinwait {
			return ErrTrezorPINNeeded
		}
		// Initialize a connection to the device
		features := new(trezor.Features)
		if _, err := w.trezorExchange(&trezor.Initialize{}, features); err != nil {
			return err
		}
		w.version = [3]uint32{features.GetMajorVersion(), features.GetMinorVersion(), features.GetPatchVersion()}
		w.label = features.GetLabel()
		w.initialized = true

		// Drop any PIN/passphrase state cached from a previous Open on this
		// physical device (see the doc comment above) so this Open starts clean.
		if _, err := w.trezorExchange(&trezor.ClearSession{}, new(trezor.Success)); err != nil {
			return err
		}

		// Do a manual ping, forcing the device to ask for its PIN and Passphrase
		askPin := true
		askPassphrase := true
		res, err := w.trezorExchange(&trezor.Ping{PinProtection: &askPin, PassphraseProtection: &askPassphrase}, new(trezor.PinMatrixRequest), new(trezor.PassphraseRequest), new(trezor.Success))
		if err != nil {
			return err
		}
		// Only return the PIN request if the device wasn't unlocked until now
		switch res {
		case 0:
			w.pinwait = true
			return ErrTrezorPINNeeded
		case 1:
			w.pinwait = false
			w.passphrasewait = true
			return ErrTrezorPassphraseNeeded
		case 2:
			return nil // responded with trezor.Success
		}
	}
	// Phase 2 requested with actual PIN entry
	if w.pinwait {
		w.pinwait = false
		res, err := w.trezorExchange(&trezor.PinMatrixAck{Pin: &passphrase}, new(trezor.Success), new(trezor.PassphraseRequest))
		if err != nil {
			w.failure = err
			return err
		}
		if res == 1 {
			w.passphrasewait = true
			return ErrTrezorPassphraseNeeded
		}
	} else if w.passphrasewait {
		w.passphrasewait = false
		if _, err := w.trezorExchange(&trezor.PassphraseAck{Passphrase: &passphrase}, new(trezor.Success)); err != nil {
			w.failure = err
			return err
		}
	}

	return nil
}

// Close implements usbwallet.driver, cleaning up and metadata maintained within
// the Trezor driver.
func (w *trezorDriver) Close() error {
	w.version, w.label, w.pinwait = [3]uint32{}, "", false
	w.initialized, w.passphrasewait = false, false
	return nil
}

// Heartbeat implements usbwallet.driver, performing a sanity check against the
// Trezor to see if it's still online.
func (w *trezorDriver) Heartbeat() error {
	if _, err := w.trezorExchange(&trezor.Ping{}, new(trezor.Success)); err != nil {
		w.failure = err
		return err
	}
	return nil
}

// Derive implements usbwallet.driver, sending a derivation request to the Trezor
// and returning the Ethereum address located on that derivation path.
func (w *trezorDriver) Derive(path accounts.DerivationPath) (common.Address, error) {
	return w.trezorDerive(path)
}

// SignTx implements usbwallet.driver, sending the transaction to the Trezor and
// waiting for the user to confirm or deny the transaction.
//
// Callisto-local: EIP-1559 dynamic-fee transactions are signed natively via the
// EthereumSignTxEIP1559 message (trezorSignEIP1559), not downgraded to legacy.
// Upstream go-ethereum's vendored trezor protobuf predates that message and only
// supports legacy signing, which is why signing an EIP-1559 tx used to fail with
// "transaction type not supported" *after* the device signed — the device signed
// a legacy tx but the resulting signature couldn't be applied back to the
// dynamic-fee tx. See trezorSignEIP1559 and encodeEthereumSignTxEIP1559.
func (w *trezorDriver) SignTx(path accounts.DerivationPath, tx *types.Transaction, chainID *big.Int) (common.Address, *types.Transaction, error) {
	if w.transport == nil {
		return common.Address{}, nil, accounts.ErrWalletClosed
	}
	if tx.Type() == types.DynamicFeeTxType {
		return w.trezorSignEIP1559(path, tx, chainID)
	}
	return w.trezorSign(path, tx, chainID)
}

func (w *trezorDriver) SignTypedMessage(path accounts.DerivationPath, domainHash []byte, messageHash []byte) ([]byte, error) {
	return nil, accounts.ErrNotSupported
}

// trezorDerive sends a derivation request to the Trezor device and returns the
// Ethereum address located on that path.
func (w *trezorDriver) trezorDerive(derivationPath []uint32) (common.Address, error) {
	address := new(trezor.EthereumAddress)
	if _, err := w.trezorExchange(&trezor.EthereumGetAddress{AddressN: derivationPath}, address); err != nil {
		return common.Address{}, err
	}
	if addr := address.GetAddressBin(); len(addr) > 0 { // Older firmwares use binary formats
		return common.BytesToAddress(addr), nil
	}
	if addr := address.GetAddressHex(); len(addr) > 0 { // Newer firmwares use hexadecimal formats
		return common.HexToAddress(addr), nil
	}
	return common.Address{}, errors.New("missing derived address")
}

// trezorSign sends the transaction to the Trezor wallet, and waits for the user
// to confirm or deny the transaction.
func (w *trezorDriver) trezorSign(derivationPath []uint32, tx *types.Transaction, chainID *big.Int) (common.Address, *types.Transaction, error) {
	// Create the transaction initiation message
	data := tx.Data()
	length := uint32(len(data))

	request := &trezor.EthereumSignTx{
		AddressN:   derivationPath,
		Nonce:      new(big.Int).SetUint64(tx.Nonce()).Bytes(),
		GasPrice:   tx.GasPrice().Bytes(),
		GasLimit:   new(big.Int).SetUint64(tx.Gas()).Bytes(),
		Value:      tx.Value().Bytes(),
		DataLength: &length,
	}
	if to := tx.To(); to != nil {
		// Non contract deploy, set recipient explicitly
		hex := to.Hex()
		request.ToHex = &hex     // Newer firmwares (old will ignore)
		request.ToBin = (*to)[:] // Older firmwares (new will ignore)
	}
	if length > 1024 { // Send the data chunked if that was requested
		request.DataInitialChunk, data = data[:1024], data[1024:]
	} else {
		request.DataInitialChunk, data = data, nil
	}
	if chainID != nil { // EIP-155 transaction, set chain ID explicitly (only 32 bit is supported!?)
		id := uint32(chainID.Int64())
		request.ChainId = &id
	}
	// Send the initiation message and stream content until a signature is returned
	response := new(trezor.EthereumTxRequest)
	if _, err := w.trezorExchange(request, response); err != nil {
		return common.Address{}, nil, err
	}
	for response.DataLength != nil && int(*response.DataLength) <= len(data) {
		chunk := data[:*response.DataLength]
		data = data[*response.DataLength:]

		if _, err := w.trezorExchange(&trezor.EthereumTxAck{DataChunk: chunk}, response); err != nil {
			return common.Address{}, nil, err
		}
	}
	// Extract the Ethereum signature and do a sanity validation
	if len(response.GetSignatureR()) == 0 || len(response.GetSignatureS()) == 0 {
		return common.Address{}, nil, errors.New("reply lacks signature")
	} else if response.GetSignatureV() == 0 && int(chainID.Int64()) <= (math.MaxUint32-36)/2 {
		// for chainId >= (MaxUint32-36)/2, Trezor returns signature bit only
		// https://github.com/trezor/trezor-mcu/pull/399
		return common.Address{}, nil, errors.New("reply lacks signature")
	}
	signature := append(append(response.GetSignatureR(), response.GetSignatureS()...), byte(response.GetSignatureV()))

	// Create the correct signer and signature transform based on the chain ID
	var signer types.Signer
	if chainID == nil {
		signer = new(types.HomesteadSigner)
	} else {
		// Trezor backend does not support typed transactions yet.
		signer = types.NewEIP155Signer(chainID)
		// if chainId is above (MaxUint32 - 36) / 2 then the final v values is returned
		// directly. Otherwise, the returned value is 35 + chainid * 2.
		if signature[64] > 1 && int(chainID.Int64()) <= (math.MaxUint32-36)/2 {
			signature[64] -= byte(chainID.Uint64()*2 + 35)
		}
	}

	// Inject the final signature into the transaction and sanity check the sender.
	// (This path handles legacy transactions; EIP-1559 dynamic-fee transactions
	// are routed to trezorSignEIP1559 by SignTx — see the fork's doc comment.)
	signed, err := tx.WithSignature(signer, signature)
	if err != nil {
		return common.Address{}, nil, err
	}
	sender, err := types.Sender(signer, signed)
	if err != nil {
		return common.Address{}, nil, err
	}
	return sender, signed, nil
}

// msgTypeEthereumSignTxEIP1559 is the Trezor wire message type for
// EthereumSignTxEIP1559 (452, from trezor-firmware common/protob/messages.proto).
// The vendored trezor protobuf package predates this message, so there is no
// trezor.MessageType_* constant or Go type for it — hence the hardcoded number
// and the hand-encoding in encodeEthereumSignTxEIP1559.
const msgTypeEthereumSignTxEIP1559 = 452

// trezorSignEIP1559 signs an EIP-1559 dynamic-fee transaction natively via the
// EthereumSignTxEIP1559 message (Callisto-local; see SignTx). The request is
// hand-encoded because the vendored trezor protobuf has no Go type for it; the
// response (EthereumTxRequest) and the data-streaming/ack loop are identical to
// legacy signing.
func (w *trezorDriver) trezorSignEIP1559(derivationPath []uint32, tx *types.Transaction, chainID *big.Int) (common.Address, *types.Transaction, error) {
	data := tx.Data()
	length := uint32(len(data))
	var initialChunk []byte
	if length > 1024 { // stream large calldata in chunks, like the legacy path
		initialChunk, data = data[:1024], data[1024:]
	} else {
		initialChunk, data = data, nil
	}

	reqBytes := encodeEthereumSignTxEIP1559(derivationPath, tx, chainID, initialChunk, length)

	response := new(trezor.EthereumTxRequest)
	if _, err := w.trezorExchangeRaw(msgTypeEthereumSignTxEIP1559, reqBytes, response); err != nil {
		return common.Address{}, nil, err
	}
	for response.DataLength != nil && int(*response.DataLength) <= len(data) {
		chunk := data[:*response.DataLength]
		data = data[*response.DataLength:]
		if _, err := w.trezorExchange(&trezor.EthereumTxAck{DataChunk: chunk}, response); err != nil {
			return common.Address{}, nil, err
		}
	}

	r, s := response.GetSignatureR(), response.GetSignatureS()
	if len(r) == 0 || len(s) == 0 {
		return common.Address{}, nil, errors.New("reply lacks signature")
	}
	// EIP-1559 uses the y-parity (0 or 1) for v — no EIP-155 offset. Trezor
	// firmware has returned this as either 0/1 or 27/28 across versions; go-
	// ethereum's dynamic-fee signer requires 0/1, so normalize the 27/28 form.
	v := response.GetSignatureV()
	if v >= 27 {
		v -= 27
	}
	// Assemble the 65-byte [R || S || V] signature go-ethereum expects,
	// left-padding R and S to 32 bytes each.
	sig := make([]byte, 65)
	copy(sig[:32], leftPad32(r))
	copy(sig[32:64], leftPad32(s))
	sig[64] = byte(v)

	signer := types.LatestSignerForChainID(chainID)
	signed, err := tx.WithSignature(signer, sig)
	if err != nil {
		return common.Address{}, nil, err
	}
	sender, err := types.Sender(signer, signed)
	if err != nil {
		return common.Address{}, nil, err
	}
	return sender, signed, nil
}

// encodeEthereumSignTxEIP1559 hand-encodes the EthereumSignTxEIP1559 protobuf
// message (field numbers/types from trezor-firmware
// common/protob/messages-ethereum.proto). Only the fields Callisto uses are
// emitted; access_list and the other optional fields are omitted (empty).
func encodeEthereumSignTxEIP1559(path []uint32, tx *types.Transaction, chainID *big.Int, initialChunk []byte, dataLength uint32) []byte {
	var b []byte
	for _, n := range path { // 1: address_n, repeated uint32
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(n))
	}
	b = appendProtoBytes(b, 2, uintBytes(tx.Nonce()))  // 2: nonce
	b = appendProtoBytes(b, 3, tx.GasFeeCap().Bytes()) // 3: max_gas_fee
	b = appendProtoBytes(b, 4, tx.GasTipCap().Bytes()) // 4: max_priority_fee
	b = appendProtoBytes(b, 5, uintBytes(tx.Gas()))    // 5: gas_limit
	if to := tx.To(); to != nil {                      // 6: to (optional string)
		b = protowire.AppendTag(b, 6, protowire.BytesType)
		b = protowire.AppendString(b, to.Hex())
	}
	b = appendProtoBytes(b, 7, tx.Value().Bytes()) // 7: value
	if len(initialChunk) > 0 {                     // 8: data_initial_chunk (optional)
		b = appendProtoBytes(b, 8, initialChunk)
	}
	b = protowire.AppendTag(b, 9, protowire.VarintType) // 9: data_length
	b = protowire.AppendVarint(b, uint64(dataLength))
	b = protowire.AppendTag(b, 10, protowire.VarintType) // 10: chain_id
	b = protowire.AppendVarint(b, chainID.Uint64())
	return b
}

// appendProtoBytes appends a length-delimited (bytes) protobuf field.
func appendProtoBytes(b []byte, num protowire.Number, val []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, val)
}

// uintBytes returns the minimal big-endian encoding of v (empty for 0), the
// format Trezor expects for its bytes-typed numeric fields — matching how the
// legacy EthereumSignTx path encodes nonce/gas_limit.
func uintBytes(v uint64) []byte {
	return new(big.Int).SetUint64(v).Bytes()
}

// leftPad32 returns b left-padded (or right-truncated) to exactly 32 bytes.
func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

// trezorExchange performs a data exchange with the Trezor wallet, sending it a
// message and retrieving the response. If multiple responses are possible, the
// method will also return the index of the destination object used.
//
// The wire framing/transport (USB HID chunking vs. Trezor Bridge HTTP) is
// delegated to w.transport; this method only marshals the request and
// interprets the response, so it works unchanged for either. Split out from
// upstream's original single USB-specific implementation — see hub.go's doc
// comment.
func (w *trezorDriver) trezorExchange(req proto.Message, results ...proto.Message) (int, error) {
	data, err := proto.Marshal(req)
	if err != nil {
		return 0, err
	}
	return w.trezorExchangeRaw(trezor.Type(req), data, results...)
}

// trezorExchangeRaw is trezorExchange with the request already marshaled and its
// message type explicit. Callisto-local: needed to send EthereumSignTxEIP1559
// (message type 452), which the vendored trezor protobuf package has no Go type
// for — encodeEthereumSignTxEIP1559 hand-encodes it and this sends it while
// reusing all the response dispatch (Failure/ButtonRequest/PassphraseRequest/
// result matching) unchanged.
func (w *trezorDriver) trezorExchangeRaw(msgType uint16, data []byte, results ...proto.Message) (int, error) {
	kind, reply, err := w.transport.exchange(msgType, data)
	if err != nil {
		return 0, err
	}
	// Try to parse the reply into the requested reply message
	if kind == uint16(trezor.MessageType_MessageType_Failure) {
		// Trezor returned a failure, extract and return the message
		failure := new(trezor.Failure)
		if err := proto.Unmarshal(reply, failure); err != nil {
			return 0, err
		}
		return 0, errors.New("trezor: " + failure.GetMessage())
	}
	if kind == uint16(trezor.MessageType_MessageType_ButtonRequest) {
		// Trezor is waiting for user confirmation, ack and wait for the next message
		return w.trezorExchange(&trezor.ButtonAck{}, results...)
	}
	// Callisto-local addition: some firmware (confirmed: Trezor Safe 5) doesn't
	// surface PassphraseRequest during the initial Open handshake the way
	// upstream's openProtocol expects — it defers the prompt to the first
	// operation that actually needs a derived key (e.g. GetAddress, SignTx). If
	// the caller wasn't deliberately checking for this reply (openProtocol's own
	// Ping call does — see the results-matching loop below, which runs first for
	// that case since PassphraseRequest is in its own results list), handle it
	// transparently and retry.
	//
	// The device answers PassphraseAck with the ORIGINAL deferred operation's
	// own reply (e.g. EthereumAddress, not a generic Success) — observed live,
	// not assumed — so this dispatches against the original results, it does
	// not resend req.
	//
	// PassphraseRequest.OnDevice (confirmed live) matters and must be respected:
	// when true, the device's own passphrase-entry security feature is active —
	// it deliberately ignores any host-supplied string and requires the user to
	// type it on the device's own screen, specifically so a compromised host
	// can never see it. Sending our own string anyway does not select a hidden
	// wallet; the device still waits for on-device entry regardless, so callers
	// must not assume the passphrase they supplied is what was actually used.
	if kind == uint16(trezor.MessageType_MessageType_PassphraseRequest) {
		if !expects(results, kind) {
			preq := new(trezor.PassphraseRequest)
			if err := proto.Unmarshal(reply, preq); err != nil {
				return 0, err
			}
			ack := &trezor.PassphraseAck{}
			if !preq.GetOnDevice() {
				pass := w.passphrase
				ack.Passphrase = &pass
			}
			// If OnDevice is true, Passphrase is left unset: the device prompts
			// the user itself and we just wait (via the normal call timeout) for
			// its reply to the original deferred operation.
			return w.trezorExchange(ack, results...)
		}
	}
	for i, res := range results {
		if trezor.Type(res) == kind {
			return i, proto.Unmarshal(reply, res)
		}
	}
	expected := make([]string, len(results))
	for i, res := range results {
		expected[i] = trezor.Name(trezor.Type(res))
	}
	return 0, fmt.Errorf("trezor: expected reply types %s, got %s", expected, trezor.Name(kind))
}

// expects reports whether kind is one of the reply types the caller explicitly
// listed in results. Callisto-local helper for trezorExchange's reactive
// PassphraseRequest handling (see above): distinguishes a deliberate check
// (e.g. openProtocol's Ping call, which lists PassphraseRequest as an expected
// outcome) from an unexpected mid-operation prompt that should be auto-handled.
func expects(results []proto.Message, kind uint16) bool {
	for _, res := range results {
		if trezor.Type(res) == kind {
			return true
		}
	}
	return false
}

// usbTrezorTransport implements trezorTransport over direct USB HID access,
// chunking the wire message into 64-byte HID reports. This is upstream's
// original (unmodified) transport logic, extracted verbatim from trezorExchange
// into its own type so trezorExchange itself is transport-agnostic — see
// hub.go's doc comment.
type usbTrezorTransport struct {
	device io.ReadWriter // USB device connection to communicate through
	log    log.Logger
}

func (t *usbTrezorTransport) exchange(msgType uint16, data []byte) (uint16, []byte, error) {
	// Construct the original message payload to chunk up
	payload := make([]byte, 8+len(data))
	copy(payload, []byte{0x23, 0x23})
	binary.BigEndian.PutUint16(payload[2:], msgType)
	binary.BigEndian.PutUint32(payload[4:], uint32(len(data)))
	copy(payload[8:], data)

	// Stream all the chunks to the device
	chunk := make([]byte, 64)
	chunk[0] = 0x3f // Report ID magic number

	for len(payload) > 0 {
		// Construct the new message to stream, padding with zeroes if needed
		if len(payload) > 63 {
			copy(chunk[1:], payload[:63])
			payload = payload[63:]
		} else {
			copy(chunk[1:], payload)
			copy(chunk[1+len(payload):], make([]byte, 63-len(payload)))
			payload = nil
		}
		// Send over to the device
		t.log.Trace("Data chunk sent to the Trezor", "chunk", hexutil.Bytes(chunk))
		if _, err := t.device.Write(chunk); err != nil {
			return 0, nil, err
		}
	}
	// Stream the reply back from the wallet in 64 byte chunks
	var (
		kind  uint16
		reply []byte
	)
	for {
		// Read the next chunk from the Trezor wallet
		if _, err := io.ReadFull(t.device, chunk); err != nil {
			return 0, nil, err
		}
		t.log.Trace("Data chunk received from the Trezor", "chunk", hexutil.Bytes(chunk))

		// Make sure the transport header matches
		if chunk[0] != 0x3f || (len(reply) == 0 && (chunk[1] != 0x23 || chunk[2] != 0x23)) {
			return 0, nil, errTrezorReplyInvalidHeader
		}
		// If it's the first chunk, retrieve the reply message type and total message length
		var payload []byte

		if len(reply) == 0 {
			kind = binary.BigEndian.Uint16(chunk[3:5])
			reply = make([]byte, 0, int(binary.BigEndian.Uint32(chunk[5:9])))
			payload = chunk[9:]
		} else {
			payload = chunk[1:]
		}
		// Append to the reply and stop when filled up
		if left := cap(reply) - len(reply); left > len(payload) {
			reply = append(reply, payload...)
		} else {
			reply = append(reply, payload[:left]...)
			break
		}
	}
	return kind, reply, nil
}
