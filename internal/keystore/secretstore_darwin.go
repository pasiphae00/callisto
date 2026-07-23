//go:build darwin

package keystore

/*
#cgo darwin CFLAGS: -fobjc-arc
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Foundation -framework Security -framework LocalAuthentication
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

// Implemented in touchid_auth_darwin.m.
extern int la_authenticate(const char* reason);

static CFStringRef ks_cfstr(const char* s) {
    return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

// ks_delete removes any generic-password item for (service, account). Missing is OK.
//
// All three functions pin kSecUseDataProtectionKeychain to false, i.e. they use
// macOS's legacy, file-based keychain rather than the newer Data Protection
// Keychain. Items in the Data Protection Keychain are gated by iOS-style
// keychain-access-groups entitlements that a Developer-ID (non-App-Store,
// unprovisioned) app cannot hold -- adding that entitlement doesn't help either, it
// makes amfid refuse to launch the binary at all (RBSRequestErrorDomain / "Launchd
// job spawn failed"). The legacy keychain has no such requirement, but as a
// consequence it also does NOT reliably enforce kSecAttrAccessControl's
// kSecAccessControlUserPresence flag on read -- verified empirically: a
// SecItemCopyMatching against such an item succeeds silently, with no Touch ID
// prompt, every time. So these items carry no access-control object at all; Touch
// ID / passcode enforcement instead happens explicitly via la_authenticate (see
// touchid_auth_darwin.m), called from Go's Get below *before* any keychain read.
static OSStatus ks_delete(const char* service, const char* account) {
    CFStringRef svc = ks_cfstr(service);
    CFStringRef acct = ks_cfstr(account);
    const void* keys[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecUseDataProtectionKeychain };
    const void* vals[] = { (const void*)kSecClassGenericPassword, svc, acct, (const void*)kCFBooleanFalse };
    CFDictionaryRef q = CFDictionaryCreate(NULL, keys, vals, 4,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus st = SecItemDelete(q);
    CFRelease(q); CFRelease(svc); CFRelease(acct);
    if (st == errSecItemNotFound) return errSecSuccess;
    return st;
}

// ks_set stores data under (service, account), this-device-only, unlocked required.
// Replaces any existing item. See ks_delete's comment for why there's no
// kSecAttrAccessControl here.
static OSStatus ks_set(const char* service, const char* account, const void* data, int len) {
    ks_delete(service, account);
    CFStringRef svc = ks_cfstr(service);
    CFStringRef acct = ks_cfstr(account);
    CFDataRef val = CFDataCreate(NULL, (const UInt8*)data, len);
    const void* keys[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData, kSecAttrAccessible, kSecUseDataProtectionKeychain };
    const void* vals[] = { (const void*)kSecClassGenericPassword, svc, acct, val, (const void*)kSecAttrAccessibleWhenUnlockedThisDeviceOnly, (const void*)kCFBooleanFalse };
    CFDictionaryRef q = CFDictionaryCreate(NULL, keys, vals, 6,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus st = SecItemAdd(q, NULL);
    CFRelease(q); CFRelease(val); CFRelease(svc); CFRelease(acct);
    return st;
}

// ks_get fetches the value for (service, account). Caller must have already run
// la_authenticate successfully -- this function itself does not gate on presence.
// On success sets *out (malloc'd, caller frees) and *outLen.
static OSStatus ks_get(const char* service, const char* account, void** out, int* outLen) {
    CFStringRef svc = ks_cfstr(service);
    CFStringRef acct = ks_cfstr(account);
    const void* keys[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecReturnData, kSecMatchLimit, kSecUseDataProtectionKeychain };
    const void* vals[] = { (const void*)kSecClassGenericPassword, svc, acct, (const void*)kCFBooleanTrue, (const void*)kSecMatchLimitOne, (const void*)kCFBooleanFalse };
    CFDictionaryRef q = CFDictionaryCreate(NULL, keys, vals, 6,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFTypeRef result = NULL;
    OSStatus st = SecItemCopyMatching(q, &result);
    CFRelease(q); CFRelease(svc); CFRelease(acct);
    if (st != errSecSuccess) return st;
    CFDataRef d = (CFDataRef)result;
    CFIndex n = CFDataGetLength(d);
    void* buf = malloc(n);
    memcpy(buf, CFDataGetBytePtr(d), n);
    *out = buf; *outLen = (int)n;
    CFRelease(result);
    return errSecSuccess;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// keychainService namespaces Callisto's keychain items.
const keychainService = "io.pasiphae.callisto"

// errSecItemNotFound is the Security framework OSStatus for a missing item.
const errSecItemNotFound = -25300

// ErrAuthenticationFailed is returned by Get when the LocalAuthentication challenge
// (Touch ID / macOS login password) is cancelled, fails, or isn't available.
var ErrAuthenticationFailed = errors.New("Touch ID / passcode authentication failed or was cancelled")

type darwinSecretStore struct{}

func osSecretStore() SecretStore { return darwinSecretStore{} }

var (
	availOnce   sync.Once
	availResult bool
)

// Available probes whether Callisto can actually create a keychain item: it stores
// and deletes a throwaway item. An ad-hoc-signed binary (no stable code identity)
// fails, so this returns false and the Touch ID UI stays hidden until Callisto is
// code-signed with a Developer ID. The probe is silent (Set doesn't prompt) and
// cached (signing state is fixed per run).
func (darwinSecretStore) Available() bool {
	availOnce.Do(func() {
		const probeRef = "__callisto_touchid_probe__"
		cs := C.CString(keychainService)
		defer C.free(unsafe.Pointer(cs))
		ca := C.CString(probeRef)
		defer C.free(unsafe.Pointer(ca))
		probe := [1]byte{0x01}
		if st := C.ks_set(cs, ca, unsafe.Pointer(&probe[0]), 1); st == 0 {
			C.ks_delete(cs, ca)
			availResult = true
		}
	})
	return availResult
}

func (darwinSecretStore) Set(ref string, value []byte) error {
	cs := C.CString(keychainService)
	defer C.free(unsafe.Pointer(cs))
	ca := C.CString(ref)
	defer C.free(unsafe.Pointer(ca))
	var dptr unsafe.Pointer
	if len(value) > 0 {
		dptr = unsafe.Pointer(&value[0])
	}
	if st := C.ks_set(cs, ca, dptr, C.int(len(value))); st != 0 {
		return fmt.Errorf("keychain set failed (OSStatus %d)", int(st))
	}
	return nil
}

// Get requires a fresh device-owner authentication (Touch ID, falling back to the
// macOS login password) before reading the keychain item. This check happens in
// Callisto's own code (touchid_auth_darwin.m) rather than via the keychain's own
// access-control gate -- see ks_delete's comment for why.
func (darwinSecretStore) Get(ref string) ([]byte, error) {
	reason := C.CString("unlock your wallet")
	defer C.free(unsafe.Pointer(reason))
	if st := C.la_authenticate(reason); st != 0 {
		return nil, ErrAuthenticationFailed
	}

	cs := C.CString(keychainService)
	defer C.free(unsafe.Pointer(cs))
	ca := C.CString(ref)
	defer C.free(unsafe.Pointer(ca))
	var out unsafe.Pointer
	var n C.int
	st := C.ks_get(cs, ca, &out, &n)
	if int(st) == errSecItemNotFound {
		return nil, ErrSecretNotFound
	}
	if st != 0 {
		return nil, fmt.Errorf("keychain get failed (OSStatus %d)", int(st))
	}
	defer C.free(out)
	return C.GoBytes(out, n), nil
}

func (darwinSecretStore) Delete(ref string) error {
	cs := C.CString(keychainService)
	defer C.free(unsafe.Pointer(cs))
	ca := C.CString(ref)
	defer C.free(unsafe.Pointer(ca))
	if st := C.ks_delete(cs, ca); st != 0 {
		return fmt.Errorf("keychain delete failed (OSStatus %d)", int(st))
	}
	return nil
}
