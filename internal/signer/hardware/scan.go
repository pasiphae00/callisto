package hardware

import (
	"fmt"

	"github.com/karalabe/usb"
)

// RawDevice is a USB device (HID or raw/libusb interface) as reported by the OS,
// independent of whether usbwallet recognizes it as a wallet. Used for
// diagnostics: when a device isn't detected, comparing what's actually enumerated
// against the VID/PID tables in usbwallet (Ledger: 0x2c97; Trezor: 0x534c/0x0001
// HID or 0x1209/0x53c1 raw WebUSB) tells us whether it's a matching problem, a
// permissions problem, or the OS not seeing the device at all.
type RawDevice struct {
	Raw          bool // raw/libusb interface (vs HID)
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
	kind := "hid"
	if d.Raw {
		kind = "raw"
	}
	return fmt.Sprintf("[%s] VID=0x%04x PID=0x%04x  iface=%d usagePage=0x%04x usage=0x%04x  %q %q  serial=%q",
		kind, d.VendorID, d.ProductID, d.Interface, d.UsagePage, d.Usage, d.Manufacturer, d.Product, d.Serial)
}

// Supported reports whether the OS/build supports USB access at all. If this is
// false, no hardware wallet of any kind can be detected — usbwallet's hub
// constructors will fail outright (see newHubs), commonly because the binary was
// built without cgo.
func Supported() bool {
	return usb.Supported()
}

// Scan enumerates every USB device the OS reports (both raw/libusb and HID
// interfaces), regardless of whether it looks like a known wallet. An empty,
// nil-error result means the OS enumerated fine but found nothing — a strong
// signal the device isn't connected/powered, or permissions are hiding it.
func Scan() ([]RawDevice, error) {
	if !usb.Supported() {
		return nil, fmt.Errorf("hardware: USB is not supported on this build/platform (was it built without cgo?)")
	}
	var out []RawDevice
	add := func(infos []usb.DeviceInfo, raw bool) {
		for _, info := range infos {
			out = append(out, RawDevice{
				Raw:          raw,
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
	}
	rawInfos, err := usb.EnumerateRaw(0, 0)
	if err != nil {
		return nil, fmt.Errorf("enumerate raw USB devices: %w", err)
	}
	add(rawInfos, true)
	hidInfos, err := usb.EnumerateHid(0, 0)
	if err != nil {
		return nil, fmt.Errorf("enumerate USB HID devices: %w", err)
	}
	add(hidInfos, false)
	return out, nil
}
