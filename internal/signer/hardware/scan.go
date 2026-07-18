package hardware

import (
	"fmt"

	"github.com/ethereum/hid"
)

// RawDevice is a USB HID device as reported by the OS, independent of whether
// go-ethereum's usbwallet recognizes it as a wallet. Used for diagnostics: when a
// device isn't detected, comparing what's actually enumerated against the
// hardcoded VID/PID tables in usbwallet (Ledger: 0x2c97; Trezor: 0x534c/0x0001
// HID or 0x1209/0x53c1 WebUSB) tells us whether it's a matching problem, a
// permissions problem, or the OS not seeing the device at all.
type RawDevice struct {
	VendorID     uint16
	ProductID    uint16
	Manufacturer string
	Product      string
	Serial       string
	Interface    int
	UsagePage    uint16
	Usage        uint16
	Path         string
}

// String formats a RawDevice for human-readable diagnostic output.
func (d RawDevice) String() string {
	return fmt.Sprintf("VID=0x%04x PID=0x%04x  iface=%d usagePage=0x%04x usage=0x%04x  %q %q  serial=%q",
		d.VendorID, d.ProductID, d.Interface, d.UsagePage, d.Usage, d.Manufacturer, d.Product, d.Serial)
}

// Supported reports whether the OS/build supports USB HID access at all. If this
// is false, no hardware wallet of any kind can be detected — usbwallet's hub
// constructors will fail outright (see newHubs), commonly because the binary was
// built without cgo, or on a platform without a HID backend.
func Supported() bool {
	return hid.Supported()
}

// Scan enumerates every USB HID device the OS reports, regardless of whether it
// looks like a known wallet. Returns an error only if enumeration itself failed
// (e.g. HID unsupported on this build); an empty, nil-error result means the OS
// enumerated fine but found no HID devices at all — a strong signal the device
// isn't connected/powered, or the OS/permissions are hiding it from this process.
func Scan() ([]RawDevice, error) {
	if !hid.Supported() {
		return nil, fmt.Errorf("hardware: USB HID is not supported on this build/platform (was it built without cgo?)")
	}
	infos, err := hid.Enumerate(0, 0) // vendorID=0, productID=0 -> match everything
	if err != nil {
		return nil, fmt.Errorf("enumerate USB HID devices: %w", err)
	}
	out := make([]RawDevice, 0, len(infos))
	for _, info := range infos {
		out = append(out, RawDevice{
			VendorID:     info.VendorID,
			ProductID:    info.ProductID,
			Manufacturer: info.Manufacturer,
			Product:      info.Product,
			Serial:       info.Serial,
			Interface:    info.Interface,
			UsagePage:    info.UsagePage,
			Usage:        info.Usage,
			Path:         info.Path,
		})
	}
	return out, nil
}
