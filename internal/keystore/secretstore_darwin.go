//go:build darwin

package keystore

/*
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static CFStringRef ks_cfstr(const char* s) {
    return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

// ks_delete removes any generic-password item for (service, account). Missing is OK.
static OSStatus ks_delete(const char* service, const char* account) {
    CFStringRef svc = ks_cfstr(service);
    CFStringRef acct = ks_cfstr(account);
    const void* keys[] = { kSecClass, kSecAttrService, kSecAttrAccount };
    const void* vals[] = { (const void*)kSecClassGenericPassword, svc, acct };
    CFDictionaryRef q = CFDictionaryCreate(NULL, keys, vals, 3,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus st = SecItemDelete(q);
    CFRelease(q); CFRelease(svc); CFRelease(acct);
    if (st == errSecItemNotFound) return errSecSuccess;
    return st;
}

// ks_set stores data under (service, account), gated by user presence (Touch ID or
// device passcode) on read, this-device-only. Replaces any existing item.
static OSStatus ks_set(const char* service, const char* account, const void* data, int len) {
    ks_delete(service, account);
    CFStringRef svc = ks_cfstr(service);
    CFStringRef acct = ks_cfstr(account);
    CFDataRef val = CFDataCreate(NULL, (const UInt8*)data, len);
    CFErrorRef acErr = NULL;
    SecAccessControlRef ac = SecAccessControlCreateWithFlags(NULL,
        kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
        kSecAccessControlUserPresence, &acErr);
    if (ac == NULL) {
        if (acErr) CFRelease(acErr);
        CFRelease(svc); CFRelease(acct); CFRelease(val);
        return errSecParam;
    }
    const void* keys[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData, kSecAttrAccessControl };
    const void* vals[] = { (const void*)kSecClassGenericPassword, svc, acct, val, ac };
    CFDictionaryRef q = CFDictionaryCreate(NULL, keys, vals, 5,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    OSStatus st = SecItemAdd(q, NULL);
    CFRelease(q); CFRelease(ac); CFRelease(val); CFRelease(svc); CFRelease(acct);
    return st;
}

// ks_get fetches the value for (service, account), prompting for user presence with
// the given prompt. On success sets *out (malloc'd, caller frees) and *outLen.
static OSStatus ks_get(const char* service, const char* account, const char* prompt, void** out, int* outLen) {
    CFStringRef svc = ks_cfstr(service);
    CFStringRef acct = ks_cfstr(account);
    CFStringRef pr = ks_cfstr(prompt);
    const void* keys[] = { kSecClass, kSecAttrService, kSecAttrAccount, kSecReturnData, kSecMatchLimit, kSecUseOperationPrompt };
    const void* vals[] = { (const void*)kSecClassGenericPassword, svc, acct, (const void*)kCFBooleanTrue, (const void*)kSecMatchLimitOne, pr };
    CFDictionaryRef q = CFDictionaryCreate(NULL, keys, vals, 6,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFTypeRef result = NULL;
    OSStatus st = SecItemCopyMatching(q, &result);
    CFRelease(q); CFRelease(svc); CFRelease(acct); CFRelease(pr);
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
	"fmt"
	"sync"
	"unsafe"
)

// keychainService namespaces Callisto's keychain items.
const keychainService = "io.pasiphae.callisto"

// errSecItemNotFound is the Security framework OSStatus for a missing item.
const errSecItemNotFound = -25300

type darwinSecretStore struct{}

func osSecretStore() SecretStore { return darwinSecretStore{} }

var (
	availOnce   sync.Once
	availResult bool
)

// Available probes whether a Touch-ID-gated keychain item can actually be created:
// it stores and deletes a throwaway access-control item. Unsigned / un-entitled
// builds fail with errSecMissingEntitlement (-34018), so this returns false and the
// Touch ID UI stays hidden until Callisto is code-signed with a Developer ID. The
// probe is silent (Set doesn't prompt) and cached (signing state is fixed per run).
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

func (darwinSecretStore) Get(ref string) ([]byte, error) {
	cs := C.CString(keychainService)
	defer C.free(unsafe.Pointer(cs))
	ca := C.CString(ref)
	defer C.free(unsafe.Pointer(ca))
	prompt := C.CString("Unlock your Callisto wallet")
	defer C.free(unsafe.Pointer(prompt))
	var out unsafe.Pointer
	var n C.int
	st := C.ks_get(cs, ca, prompt, &out, &n)
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
