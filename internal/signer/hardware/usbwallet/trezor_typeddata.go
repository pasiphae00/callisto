package usbwallet

// Native Trezor EIP-712 typed-data signing via the streaming EthereumSignTypedData
// flow (messages 464-469). Unlike EthereumSignTypedHash (470), which is an
// experimental firmware message the device rejects by default, this works on
// stock firmware and shows the decoded data on-device. The vendored trezor
// protobuf has none of these message types, so — as with the EIP-1559 fix — the
// requests are hand-encoded and replies hand-decoded with protowire.
//
// Flow: send EthereumSignTypedData{primary_type} → the device drives the rest,
// asking for struct definitions (EthereumTypedDataStructRequest → …StructAck) and
// field values by path (EthereumTypedDataValueRequest → …ValueAck), then returns
// EthereumTypedDataSignature. Value encoding follows trezor-firmware: atomics to
// their ABI byte width, arrays as a uint16 element count (the device then requests
// each element by extending the member path).

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"google.golang.org/protobuf/encoding/protowire"
)

// Trezor wire message types for the streaming EIP-712 flow (messages.proto).
const (
	msgTypeEthereumSignTypedData      = 464
	msgTypeEthereumTypedDataStructReq = 465
	msgTypeEthereumTypedDataStructAck = 466
	msgTypeEthereumTypedDataValueReq  = 467
	msgTypeEthereumTypedDataValueAck  = 468
	// 469 = EthereumTypedDataSignature (see msgTypeEthereumTypedDataSignature).
)

// EthereumDataType enum values (EthereumTypedDataStructAck.EthereumDataType).
const (
	edtUint    = 1
	edtInt     = 2
	edtBytes   = 3
	edtString  = 4
	edtBool    = 5
	edtAddress = 6
	edtArray   = 7
	edtStruct  = 8
)

// typedDataDoc is a parsed EIP-712 document. Numbers in domain/message are decoded
// as json.Number (via UseNumber) so large uint256 values keep full precision.
type typedDataDoc struct {
	Types       map[string][]typedField `json:"types"`
	PrimaryType string                  `json:"primaryType"`
	Domain      map[string]interface{}  `json:"domain"`
	Message     map[string]interface{}  `json:"message"`
}

type typedField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func parseTypedData(raw []byte) (*typedDataDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var td typedDataDoc
	if err := dec.Decode(&td); err != nil {
		return nil, fmt.Errorf("trezor: parse typed data: %w", err)
	}
	if td.PrimaryType == "" || td.Types == nil {
		return nil, errors.New("trezor: typed data missing primaryType/types")
	}
	return &td, nil
}

// fieldType is a parsed EIP-712 field type (solidity type string → Trezor's
// EthereumFieldType model).
type fieldType struct {
	dataType   int
	size       uint32 // uintN/intN and bytesN width (bytes); fixed-array length
	hasSize    bool
	entryType  *fieldType // element type for arrays
	structName string     // referenced struct name
}

// parseFieldType parses a solidity type string ("uint256", "address", "bytes32",
// "Foo", "Foo[]", "Foo[3]") into a fieldType. Struct names must exist in types.
func parseFieldType(typ string, types map[string][]typedField) (*fieldType, error) {
	if strings.HasSuffix(typ, "]") {
		open := strings.LastIndexByte(typ, '[')
		if open < 0 {
			return nil, fmt.Errorf("trezor: bad array type %q", typ)
		}
		entry, err := parseFieldType(typ[:open], types)
		if err != nil {
			return nil, err
		}
		ft := &fieldType{dataType: edtArray, entryType: entry}
		if idx := typ[open+1 : len(typ)-1]; idx != "" {
			n, err := strconv.Atoi(idx)
			if err != nil {
				return nil, fmt.Errorf("trezor: bad array size %q", typ)
			}
			ft.size, ft.hasSize = uint32(n), true
		}
		return ft, nil
	}
	switch {
	case typ == "address":
		return &fieldType{dataType: edtAddress}, nil
	case typ == "bool":
		return &fieldType{dataType: edtBool}, nil
	case typ == "string":
		return &fieldType{dataType: edtString}, nil
	case typ == "bytes":
		return &fieldType{dataType: edtBytes}, nil
	case strings.HasPrefix(typ, "bytes"):
		n, err := strconv.Atoi(typ[len("bytes"):])
		if err != nil {
			return nil, fmt.Errorf("trezor: bad bytesN %q", typ)
		}
		return &fieldType{dataType: edtBytes, size: uint32(n), hasSize: true}, nil
	case strings.HasPrefix(typ, "uint"):
		return intType(edtUint, typ[len("uint"):])
	case strings.HasPrefix(typ, "int"):
		return intType(edtInt, typ[len("int"):])
	default:
		if _, ok := types[typ]; ok {
			return &fieldType{dataType: edtStruct, structName: typ}, nil
		}
		return nil, fmt.Errorf("trezor: unknown type %q", typ)
	}
}

func intType(dataType int, bitsStr string) (*fieldType, error) {
	bits := 256
	if bitsStr != "" {
		var err error
		if bits, err = strconv.Atoi(bitsStr); err != nil || bits <= 0 || bits%8 != 0 {
			return nil, fmt.Errorf("trezor: bad integer width %q", bitsStr)
		}
	}
	return &fieldType{dataType: dataType, size: uint32(bits / 8), hasSize: true}, nil
}

// --- protobuf encoding (requests we send) -----------------------------------

// encodeSignTypedData encodes EthereumSignTypedData (464): address_n(1), primary
// _type(2), metamask_v4_compat(3)=true.
func encodeSignTypedData(path []uint32, primaryType string) []byte {
	var b []byte
	for _, n := range path {
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(n))
	}
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, primaryType)
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, 1) // metamask_v4_compat
	return b
}

// encodeFieldType encodes an EthereumFieldType: data_type(1), size(2), entry_type
// (3, nested), struct_name(4).
func encodeFieldType(ft *fieldType) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(ft.dataType))
	if ft.hasSize {
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(ft.size))
	}
	if ft.entryType != nil {
		b = appendProtoBytes(b, 3, encodeFieldType(ft.entryType))
	}
	if ft.structName != "" {
		b = protowire.AppendTag(b, 4, protowire.BytesType)
		b = protowire.AppendString(b, ft.structName)
	}
	return b
}

// encodeStructAck encodes EthereumTypedDataStructAck: repeated members(1), each an
// EthereumStructMember{type(1), name(2)}.
func encodeStructAck(members []typedField, types map[string][]typedField) ([]byte, error) {
	var b []byte
	for _, m := range members {
		ft, err := parseFieldType(m.Type, types)
		if err != nil {
			return nil, err
		}
		var member []byte
		member = appendProtoBytes(member, 1, encodeFieldType(ft)) // type
		member = protowire.AppendTag(member, 2, protowire.BytesType)
		member = protowire.AppendString(member, m.Name) // name
		b = appendProtoBytes(b, 1, member)
	}
	return b, nil
}

// encodeValueAck encodes EthereumTypedDataValueAck: value(1) bytes.
func encodeValueAck(value []byte) []byte {
	return appendProtoBytes(nil, 1, value)
}

// --- protobuf decoding (requests we receive) --------------------------------

// decodeStructRequest reads EthereumTypedDataStructRequest.name (field 1).
func decodeStructRequest(b []byte) (string, error) {
	var name string
	err := scanProto(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) {
		if num == 1 && typ == protowire.BytesType {
			name = string(v)
		}
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

// decodeValueRequest reads EthereumTypedDataValueRequest.member_path (field 1,
// repeated uint32).
func decodeValueRequest(b []byte) ([]uint32, error) {
	var path []uint32
	err := scanProto(b, func(num protowire.Number, typ protowire.Type, _ []byte, varint uint64) {
		if num == 1 && typ == protowire.VarintType {
			path = append(path, uint32(varint))
		}
	})
	if err != nil {
		return nil, err
	}
	return path, nil
}

// scanProto walks a flat protobuf, invoking fn for each field (bytes fields pass
// v, varint fields pass varint). It skips field types it doesn't hand to fn.
func scanProto(b []byte, fn func(num protowire.Number, typ protowire.Type, bytesVal []byte, varintVal uint64)) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, vn := protowire.ConsumeVarint(b)
			if vn < 0 {
				return protowire.ParseError(vn)
			}
			fn(num, typ, nil, v)
			b = b[vn:]
		case protowire.BytesType:
			v, vn := protowire.ConsumeBytes(b)
			if vn < 0 {
				return protowire.ParseError(vn)
			}
			fn(num, typ, v, 0)
			b = b[vn:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return nil
}

// --- value navigation + encoding --------------------------------------------

// valueAt resolves the value the device asked for at member_path and encodes it.
// member_path[0] selects the root (0 = EIP712Domain, 1 = primaryType); the rest
// navigate structs (by member index) and arrays (by element index).
func (td *typedDataDoc) valueAt(memberPath []uint32) ([]byte, error) {
	if len(memberPath) == 0 {
		return nil, errors.New("trezor: empty member path")
	}
	var structName string
	var value interface{}
	switch memberPath[0] {
	case 0:
		structName, value = "EIP712Domain", td.Domain
	case 1:
		structName, value = td.PrimaryType, td.Message
	default:
		return nil, fmt.Errorf("trezor: invalid root index %d", memberPath[0])
	}

	curFt := &fieldType{dataType: edtStruct, structName: structName}
	curVal := value
	for _, idx := range memberPath[1:] {
		switch curFt.dataType {
		case edtStruct:
			members := td.Types[curFt.structName]
			if int(idx) >= len(members) {
				return nil, fmt.Errorf("trezor: member %d out of range in %q", idx, curFt.structName)
			}
			m := members[idx]
			ft, err := parseFieldType(m.Type, td.Types)
			if err != nil {
				return nil, err
			}
			mv, _ := curVal.(map[string]interface{})
			curFt, curVal = ft, mv[m.Name]
		case edtArray:
			arr, ok := curVal.([]interface{})
			if !ok || int(idx) >= len(arr) {
				return nil, fmt.Errorf("trezor: array index %d out of range", idx)
			}
			curFt, curVal = curFt.entryType, arr[idx]
		default:
			return nil, errors.New("trezor: cannot descend into an atomic field")
		}
	}
	return encodeValue(curFt, curVal)
}

// encodeValue encodes a single field value to the bytes Trezor expects.
func encodeValue(ft *fieldType, value interface{}) ([]byte, error) {
	switch ft.dataType {
	case edtArray:
		arr, ok := value.([]interface{})
		if !ok {
			return nil, fmt.Errorf("trezor: array value is %T", value)
		}
		out := make([]byte, 2)
		binary.BigEndian.PutUint16(out, uint16(len(arr)))
		return out, nil
	case edtStruct:
		return nil, errors.New("trezor: unexpected value request for a struct")
	case edtBool:
		if b, _ := value.(bool); b {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case edtString:
		s, _ := value.(string)
		return []byte(s), nil
	case edtAddress:
		s, _ := value.(string)
		return common.HexToAddress(s).Bytes(), nil
	case edtBytes:
		s, _ := value.(string)
		raw, err := hexutil.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("trezor: bad bytes value: %w", err)
		}
		if ft.hasSize {
			out := make([]byte, ft.size)
			copy(out, raw)
			return out, nil
		}
		return raw, nil
	case edtUint, edtInt:
		bi, err := toBigInt(value)
		if err != nil {
			return nil, err
		}
		return leftPadTo(bi.Bytes(), int(ft.size)), nil
	default:
		return nil, fmt.Errorf("trezor: unsupported data type %d", ft.dataType)
	}
}

func toBigInt(v interface{}) (*big.Int, error) {
	switch n := v.(type) {
	case json.Number:
		bi, ok := new(big.Int).SetString(n.String(), 10)
		if !ok {
			return nil, fmt.Errorf("trezor: bad number %q", n.String())
		}
		return bi, nil
	case string:
		base := 10
		if strings.HasPrefix(n, "0x") || strings.HasPrefix(n, "0X") {
			base = 0 // let SetString detect the 0x prefix
		}
		bi, ok := new(big.Int).SetString(n, base)
		if !ok {
			return nil, fmt.Errorf("trezor: bad numeric string %q", n)
		}
		return bi, nil
	case float64:
		return big.NewInt(int64(n)), nil
	default:
		return nil, fmt.Errorf("trezor: cannot convert %T to integer", v)
	}
}

func leftPadTo(b []byte, n int) []byte {
	if len(b) >= n {
		return b[len(b)-n:]
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

// signTypedDataStreaming runs the full EthereumSignTypedData flow and returns the
// 65-byte signature. Button/passphrase prompts are handled by rawExchange.
func (w *trezorDriver) signTypedDataStreaming(path accounts.DerivationPath, typedDataJSON []byte) ([]byte, error) {
	if w.transport == nil {
		return nil, accounts.ErrWalletClosed
	}
	td, err := parseTypedData(typedDataJSON)
	if err != nil {
		return nil, err
	}
	kind, reply, err := w.rawExchange(msgTypeEthereumSignTypedData, encodeSignTypedData(path, td.PrimaryType))
	if err != nil {
		return nil, err
	}
	for {
		switch kind {
		case msgTypeEthereumTypedDataStructReq:
			name, derr := decodeStructRequest(reply)
			if derr != nil {
				return nil, derr
			}
			members, ok := td.Types[name]
			if !ok {
				return nil, fmt.Errorf("trezor: device asked for unknown struct %q", name)
			}
			ack, eerr := encodeStructAck(members, td.Types)
			if eerr != nil {
				return nil, eerr
			}
			if kind, reply, err = w.rawExchange(msgTypeEthereumTypedDataStructAck, ack); err != nil {
				return nil, err
			}
		case msgTypeEthereumTypedDataValueReq:
			memberPath, derr := decodeValueRequest(reply)
			if derr != nil {
				return nil, derr
			}
			val, verr := td.valueAt(memberPath)
			if verr != nil {
				return nil, verr
			}
			if kind, reply, err = w.rawExchange(msgTypeEthereumTypedDataValueAck, encodeValueAck(val)); err != nil {
				return nil, err
			}
		case msgTypeEthereumTypedDataSignature:
			sig, derr := decodeTypedDataSignature(reply)
			if derr != nil {
				return nil, derr
			}
			if len(sig) != 65 {
				return nil, fmt.Errorf("trezor: unexpected typed-data signature length %d", len(sig))
			}
			return sig, nil
		default:
			return nil, fmt.Errorf("trezor: unexpected reply type %d during typed-data signing", kind)
		}
	}
}
