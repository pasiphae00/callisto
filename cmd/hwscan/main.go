// Command hwscan is a diagnostic tool for hardware-wallet detection issues: it
// lists every USB HID device the OS reports, independent of whether Callisto
// recognizes it as a wallet. Run it with your device connected and unlocked:
//
//	go run ./cmd/hwscan
//
// If your device doesn't appear at all, the OS/driver isn't exposing it to this
// process (permissions, another app holding it exclusively, or a cable/port
// issue) — that's a system-level problem, not a Callisto one. If it does appear,
// compare its VID/PID against the values printed below to see whether Callisto's
// device table needs updating for your model/firmware.
package main

import (
	"fmt"
	"os"

	"codeberg.org/pasiphae/callisto/internal/signer/hardware"
)

// knownVendors documents the VID/PID combinations go-ethereum's usbwallet
// currently matches, so scan output can be compared against them directly.
var knownVendors = []string{
	"Ledger:  VID=0x2c97  PID upper-byte in {0x00,0x01,0x04,0x05,0x06,0x07,0x08} (model) with low byte as interface bitfield",
	"Trezor (HID, older firmware):     VID=0x534c PID=0x0001",
	"Trezor (WebUSB, firmware >1.8.0): VID=0x1209 PID=0x53c1",
}

func main() {
	fmt.Println("Callisto hardware-wallet USB scan")
	fmt.Println("==================================")
	fmt.Printf("USB supported on this build: %v\n\n", hardware.Supported())

	devices, err := hardware.Scan()
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan failed:", err)
		os.Exit(1)
	}

	if len(devices) == 0 {
		fmt.Println("No USB devices found at all (raw or HID).")
		fmt.Println("This means the OS isn't exposing ANY matching device to this process —")
		fmt.Println("check the device is connected/unlocked, and that no other app")
		fmt.Println("(Trezor Suite, Trezor Bridge, Ledger Live) is holding it exclusively.")
		return
	}

	fmt.Printf("Found %d USB HID device(s):\n\n", len(devices))
	for _, d := range devices {
		fmt.Println(" ", d)
	}

	fmt.Println("\nCallisto/go-ethereum currently recognizes:")
	for _, v := range knownVendors {
		fmt.Println("  -", v)
	}
	fmt.Println("\nIf your device is listed above but its VID/PID doesn't match any of")
	fmt.Println("these, that's the bug — please share this output so the matching")
	fmt.Println("table can be updated for your device/firmware.")
}
