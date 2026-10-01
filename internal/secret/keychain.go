//go:build darwin && cgo

package secret

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>

static CFTypeRef matchLimitAll(void) { return kSecMatchLimitAll; }
static CFTypeRef matchLimitOne(void) { return kSecMatchLimitOne; }
static CFTypeRef authUIFail(void) { return kSecUseAuthenticationUIFail; }

// dictSet keeps CFDictionarySetValue out of Go so vet does not flag
// unsafe.Pointer conversions of CFTypeRef constants.
static void dictSet(CFMutableDictionaryRef d, CFTypeRef key, CFTypeRef val) {
	CFDictionarySetValue(d, key, val);
}

static CFTypeRef dictGet(CFDictionaryRef d, CFTypeRef key) {
	return CFDictionaryGetValue(d, key);
}

// makeKeychainArray2 keeps CFArrayCreate of SecKeychainRef values out of Go
// so vet does not flag unsafe.Pointer conversions.
static CFArrayRef makeKeychainArray2(SecKeychainRef a, SecKeychainRef b) {
	const void *values[2] = { a, b };
	return CFArrayCreate(kCFAllocatorDefault, values, 2, &kCFTypeArrayCallBacks);
}

static CFArrayRef makeKeychainArray1(SecKeychainRef a) {
	const void *values[1] = { a };
	return CFArrayCreate(kCFAllocatorDefault, values, 1, &kCFTypeArrayCallBacks);
}
*/
import "C"

import (
	"context"
	"fmt"
	"sort"
	"time"
	"unsafe"
)

// Keychain stores secrets in the macOS keychain as generic passwords.
//
// Identity maps onto kSecAttrService + kSecAttrAccount. The config path is
// folded into the account attribute so two config files cannot share a
// secret (same guarantee as the libsecret attribute set).
type Keychain struct {
	// scoped is set for tests that point the process search list at a
	// throwaway keychain. prevSearch is restored on destroyFileKeychain.
	scoped     bool
	prevSearch C.CFArrayRef
	keychain   C.SecKeychainRef
}

// NewKeychain returns a store that talks to the default keychain search list.
func NewKeychain() *Keychain { return &Keychain{} }

// accountAttr folds Config into kSecAttrAccount for isolation.
func accountAttr(id Identity) string {
	return id.ProfileID + "\x1f" + id.Config
}

func (k *Keychain) Presence(ctx context.Context, id Identity) (Presence, error) {
	if err := ctx.Err(); err != nil {
		return NotSaved, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	query := k.baseQuery(id)
	defer C.CFRelease(C.CFTypeRef(query))
	C.dictSet(query, C.CFTypeRef(C.kSecMatchLimit), C.CFTypeRef(C.matchLimitOne()))
	C.dictSet(query, C.CFTypeRef(C.kSecReturnAttributes), C.CFTypeRef(C.kCFBooleanTrue))
	// Never unlock or prompt just because the list cursor moved.
	C.dictSet(query, C.CFTypeRef(C.kSecUseAuthenticationUI), C.CFTypeRef(C.authUIFail()))

	var result C.CFTypeRef
	status := C.SecItemCopyMatching(C.CFDictionaryRef(query), &result)
	if status == C.errSecItemNotFound {
		return NotSaved, nil
	}
	if status == C.errSecInteractionNotAllowed || status == C.errSecAuthFailed || status == C.errSecUserCanceled {
		// Item may exist but we must not prompt; treat as saved so the UI
		// does not claim "not saved" for a locked item.
		return Saved, nil
	}
	if err := mapSecStatus(ctx, status); err != nil {
		return NotSaved, err
	}
	C.CFRelease(result)
	return Saved, nil
}

func (k *Keychain) Lookup(ctx context.Context, id Identity) (LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return LookupResult{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	// Attributes + persistent refs with MatchLimitAll. Combining MatchLimitAll
	// with kSecReturnData returns errSecParam on current macOS, so fetch the
	// password for the newest hit in a second query by that item's persistent
	// ref (MatchLimitOne alone is not guaranteed to return the newest).
	query := k.baseQuery(id)
	defer C.CFRelease(C.CFTypeRef(query))
	C.dictSet(query, C.CFTypeRef(C.kSecMatchLimit), C.CFTypeRef(C.matchLimitAll()))
	C.dictSet(query, C.CFTypeRef(C.kSecReturnAttributes), C.CFTypeRef(C.kCFBooleanTrue))
	C.dictSet(query, C.CFTypeRef(C.kSecReturnPersistentRef), C.CFTypeRef(C.kCFBooleanTrue))

	var result C.CFTypeRef
	status := C.SecItemCopyMatching(C.CFDictionaryRef(query), &result)
	if status == C.errSecItemNotFound {
		return LookupResult{}, ErrNotFound
	}
	if err := mapSecStatus(ctx, status); err != nil {
		return LookupResult{}, err
	}
	defer C.CFRelease(result)

	type hit struct {
		mod  time.Time
		pref C.CFDataRef
	}
	var hits []hit
	switch C.CFGetTypeID(result) {
	case C.CFArrayGetTypeID():
		arr := C.CFArrayRef(result)
		n := int(C.CFArrayGetCount(arr))
		for i := 0; i < n; i++ {
			dict := C.CFDictionaryRef(C.CFArrayGetValueAtIndex(arr, C.CFIndex(i)))
			pref := persistentRef(dict)
			if pref == 0 {
				continue
			}
			hits = append(hits, hit{mod: modDate(dict), pref: pref})
		}
	case C.CFDictionaryGetTypeID():
		dict := C.CFDictionaryRef(result)
		pref := persistentRef(dict)
		if pref != 0 {
			hits = append(hits, hit{mod: modDate(dict), pref: pref})
		}
	default:
		return LookupResult{}, fmt.Errorf("%w: unexpected keychain result type", ErrUnavailable)
	}
	if len(hits) == 0 {
		return LookupResult{}, ErrNotFound
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })

	pw, err := k.copyPasswordByPersistentRef(ctx, hits[0].pref)
	if err != nil {
		return LookupResult{}, err
	}
	return LookupResult{Password: pw, Multiple: len(hits) > 1}, nil
}

func persistentRef(dict C.CFDictionaryRef) C.CFDataRef {
	ref := C.dictGet(dict, C.CFTypeRef(C.kSecValuePersistentRef))
	if ref == 0 {
		return 0
	}
	return C.CFDataRef(ref)
}

func (k *Keychain) copyPasswordByPersistentRef(ctx context.Context, pref C.CFDataRef) (Password, error) {
	query := C.CFDictionaryCreateMutable(C.kCFAllocatorDefault, 0, &C.kCFTypeDictionaryKeyCallBacks, &C.kCFTypeDictionaryValueCallBacks)
	if query == 0 {
		return Password{}, fmt.Errorf("%w: CFDictionaryCreateMutable", ErrUnavailable)
	}
	defer C.CFRelease(C.CFTypeRef(query))
	C.dictSet(query, C.CFTypeRef(C.kSecClass), C.CFTypeRef(C.kSecClassGenericPassword))
	C.dictSet(query, C.CFTypeRef(C.kSecValuePersistentRef), C.CFTypeRef(pref))
	C.dictSet(query, C.CFTypeRef(C.kSecMatchLimit), C.CFTypeRef(C.matchLimitOne()))
	C.dictSet(query, C.CFTypeRef(C.kSecReturnData), C.CFTypeRef(C.kCFBooleanTrue))

	var result C.CFTypeRef
	status := C.SecItemCopyMatching(C.CFDictionaryRef(query), &result)
	if status == C.errSecItemNotFound {
		return Password{}, ErrNotFound
	}
	if err := mapSecStatus(ctx, status); err != nil {
		return Password{}, err
	}
	defer C.CFRelease(result)
	data := C.CFDataRef(result)
	n := C.CFDataGetLength(data)
	var secret []byte
	if n > 0 {
		secret = C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(data)), C.int(n))
	}
	pw, err := NewPassword(string(secret))
	clear(secret)
	if err != nil {
		return Password{}, err
	}
	return pw, nil
}

func (k *Keychain) Upsert(ctx context.Context, id Identity, pw Password) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	secret := []byte(pw.v)
	defer clear(secret)
	data := cfData(secret)
	if data == 0 {
		return fmt.Errorf("%w: CFDataCreate", ErrUnavailable)
	}
	defer C.CFRelease(C.CFTypeRef(data))

	query := k.baseQuery(id)
	defer C.CFRelease(C.CFTypeRef(query))

	attrs := C.CFDictionaryCreateMutable(C.kCFAllocatorDefault, 1, &C.kCFTypeDictionaryKeyCallBacks, &C.kCFTypeDictionaryValueCallBacks)
	if attrs == 0 {
		return fmt.Errorf("%w: CFDictionaryCreateMutable", ErrUnavailable)
	}
	defer C.CFRelease(C.CFTypeRef(attrs))
	C.dictSet(attrs, C.CFTypeRef(C.kSecValueData), C.CFTypeRef(data))

	status := C.SecItemUpdate(C.CFDictionaryRef(query), C.CFDictionaryRef(attrs))
	if status == C.errSecSuccess {
		return nil
	}
	if status != C.errSecItemNotFound {
		return mapSecStatus(ctx, status)
	}

	add := k.baseQuery(id)
	defer C.CFRelease(C.CFTypeRef(add))
	// File-keychain tests: SecItemAdd without kSecUseKeychain can land in the
	// data-protection keychain even when the search list is scoped.
	if k.keychain != 0 {
		C.dictSet(add, C.CFTypeRef(C.kSecUseKeychain), C.CFTypeRef(k.keychain))
	}
	label := cfString(id.Label())
	defer C.CFRelease(C.CFTypeRef(label))
	C.dictSet(add, C.CFTypeRef(C.kSecAttrLabel), C.CFTypeRef(label))
	C.dictSet(add, C.CFTypeRef(C.kSecValueData), C.CFTypeRef(data))
	st := C.SecItemAdd(C.CFDictionaryRef(add), nil)
	return mapSecStatus(ctx, st)
}

func (k *Keychain) Delete(ctx context.Context, id Identity) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	query := k.baseQuery(id)
	defer C.CFRelease(C.CFTypeRef(query))
	status := C.SecItemDelete(C.CFDictionaryRef(query))
	if status == C.errSecItemNotFound {
		return ErrNotFound
	}
	return mapSecStatus(ctx, status)
}

// baseQuery builds a mutable dictionary identifying a generic-password item.
// Caller must CFRelease the result.
func (k *Keychain) baseQuery(id Identity) C.CFMutableDictionaryRef {
	query := C.CFDictionaryCreateMutable(C.kCFAllocatorDefault, 0, &C.kCFTypeDictionaryKeyCallBacks, &C.kCFTypeDictionaryValueCallBacks)
	C.dictSet(query, C.CFTypeRef(C.kSecClass), C.CFTypeRef(C.kSecClassGenericPassword))

	service := cfString(id.Service)
	C.dictSet(query, C.CFTypeRef(C.kSecAttrService), C.CFTypeRef(service))
	C.CFRelease(C.CFTypeRef(service))

	account := cfString(accountAttr(id))
	C.dictSet(query, C.CFTypeRef(C.kSecAttrAccount), C.CFTypeRef(account))
	C.CFRelease(C.CFTypeRef(account))

	// File-keychain tests: pin match/update/delete to the throwaway keychain
	// so Upsert does not update a same-identity item in another search-list
	// keychain (and so Presence/Lookup stay isolated). SecItemAdd still needs
	// kSecUseKeychain separately — without it, adds can land in the
	// data-protection keychain even when the search list is scoped.
	if k.keychain != 0 {
		arr := C.makeKeychainArray1(k.keychain)
		if arr != 0 {
			C.dictSet(query, C.CFTypeRef(C.kSecMatchSearchList), C.CFTypeRef(arr))
			C.CFRelease(C.CFTypeRef(arr))
		}
	}
	return query
}

func cfString(s string) C.CFStringRef {
	if len(s) == 0 {
		return C.CFStringCreateWithBytes(C.kCFAllocatorDefault, nil, 0, C.kCFStringEncodingUTF8, C.false)
	}
	return C.CFStringCreateWithBytes(
		C.kCFAllocatorDefault,
		(*C.UInt8)(unsafe.Pointer(unsafe.StringData(s))),
		C.CFIndex(len(s)),
		C.kCFStringEncodingUTF8,
		C.false,
	)
}

func cfData(b []byte) C.CFDataRef {
	if len(b) == 0 {
		return C.CFDataCreate(C.kCFAllocatorDefault, nil, 0)
	}
	return C.CFDataCreate(C.kCFAllocatorDefault, (*C.UInt8)(unsafe.Pointer(&b[0])), C.CFIndex(len(b)))
}

func modDate(dict C.CFDictionaryRef) time.Time {
	if dateRef := C.dictGet(dict, C.CFTypeRef(C.kSecAttrModificationDate)); dateRef != 0 {
		secs := C.CFDateGetAbsoluteTime(C.CFDateRef(dateRef))
		return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(float64(secs) * float64(time.Second)))
	}
	return time.Time{}
}

func mapSecStatus(ctx context.Context, status C.OSStatus) error {
	if status == C.errSecSuccess {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, ctxErr)
	}
	switch status {
	case C.errSecItemNotFound:
		return ErrNotFound
	case C.errSecUserCanceled, C.errSecAuthFailed, C.errSecInteractionNotAllowed:
		// Dismissals and auth refusals must not look like "no password saved".
		return fmt.Errorf("%w: %s", ErrUnavailable, secMessage(status))
	default:
		return fmt.Errorf("%w: %s", ErrUnavailable, secMessage(status))
	}
}

func secMessage(status C.OSStatus) string {
	if msg := C.SecCopyErrorMessageString(status, nil); msg != 0 {
		defer C.CFRelease(C.CFTypeRef(msg))
		return cfStringToGo(msg)
	}
	return fmt.Sprintf("OSStatus %d", int(status))
}

func cfStringToGo(s C.CFStringRef) string {
	n := C.CFStringGetLength(s)
	if n == 0 {
		return ""
	}
	max := C.CFStringGetMaximumSizeForEncoding(n, C.kCFStringEncodingUTF8) + 1
	buf := (*C.char)(C.malloc(C.size_t(max)))
	if buf == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(buf))
	if C.CFStringGetCString(s, buf, max, C.kCFStringEncodingUTF8) == 0 {
		return ""
	}
	return C.GoString(buf)
}

// createUnlockedKeychainFile creates an unlocked keychain at path without
// changing the process search list. Caller must SecKeychainDelete + CFRelease.
func createUnlockedKeychainFile(path string, pass []byte) (C.SecKeychainRef, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var ref C.SecKeychainRef
	status := C.SecKeychainCreate(
		cpath,
		C.UInt32(len(pass)),
		unsafe.Pointer(&pass[0]),
		0,
		0,
		&ref,
	)
	if status != C.errSecSuccess {
		return 0, fmt.Errorf("SecKeychainCreate: %s", secMessage(status))
	}
	status = C.SecKeychainUnlock(ref, C.UInt32(len(pass)), unsafe.Pointer(&pass[0]), C.true)
	if status != C.errSecSuccess {
		C.SecKeychainDelete(ref)
		C.CFRelease(C.CFTypeRef(ref))
		return 0, fmt.Errorf("SecKeychainUnlock: %s", secMessage(status))
	}
	return ref, nil
}

// newMultiFileKeychain points the process search list at two unlocked file
// keychains so Lookup can see Multiple matches. older/newer Upsert into each
// file via kSecUseKeychain; lookup searches the list with no UseKeychain pin.
// cleanup restores the previous search list and deletes both files.
func newMultiFileKeychain(pathOlder, pathNewer string, pass []byte) (lookup, older, newer *Keychain, cleanup func(), err error) {
	refOlder, err := createUnlockedKeychainFile(pathOlder, pass)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	refNewer, err := createUnlockedKeychainFile(pathNewer, pass)
	if err != nil {
		C.SecKeychainDelete(refOlder)
		C.CFRelease(C.CFTypeRef(refOlder))
		return nil, nil, nil, nil, err
	}
	var prev C.CFArrayRef
	status := C.SecKeychainCopySearchList(&prev)
	if status != C.errSecSuccess {
		C.SecKeychainDelete(refOlder)
		C.CFRelease(C.CFTypeRef(refOlder))
		C.SecKeychainDelete(refNewer)
		C.CFRelease(C.CFTypeRef(refNewer))
		return nil, nil, nil, nil, fmt.Errorf("SecKeychainCopySearchList: %s", secMessage(status))
	}
	arr := C.makeKeychainArray2(refOlder, refNewer)
	if arr == 0 {
		C.CFRelease(C.CFTypeRef(prev))
		C.SecKeychainDelete(refOlder)
		C.CFRelease(C.CFTypeRef(refOlder))
		C.SecKeychainDelete(refNewer)
		C.CFRelease(C.CFTypeRef(refNewer))
		return nil, nil, nil, nil, fmt.Errorf("CFArrayCreate")
	}
	status = C.SecKeychainSetSearchList(arr)
	C.CFRelease(C.CFTypeRef(arr))
	if status != C.errSecSuccess {
		C.CFRelease(C.CFTypeRef(prev))
		C.SecKeychainDelete(refOlder)
		C.CFRelease(C.CFTypeRef(refOlder))
		C.SecKeychainDelete(refNewer)
		C.CFRelease(C.CFTypeRef(refNewer))
		return nil, nil, nil, nil, fmt.Errorf("SecKeychainSetSearchList: %s", secMessage(status))
	}
	lookup = &Keychain{}
	older = &Keychain{keychain: refOlder}
	newer = &Keychain{keychain: refNewer}
	cleanup = func() {
		C.SecKeychainSetSearchList(prev)
		C.CFRelease(C.CFTypeRef(prev))
		C.SecKeychainDelete(refOlder)
		C.CFRelease(C.CFTypeRef(refOlder))
		C.SecKeychainDelete(refNewer)
		C.CFRelease(C.CFTypeRef(refNewer))
	}
	return lookup, older, newer, cleanup, nil
}

// newFileKeychain creates an unlocked keychain file and points the process
// search list at it alone, so tests never touch the login keychain. Caller
// must call destroyFileKeychain when finished (restores the search list).
func newFileKeychain(path string, pass []byte) (*Keychain, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var ref C.SecKeychainRef
	status := C.SecKeychainCreate(
		cpath,
		C.UInt32(len(pass)),
		unsafe.Pointer(&pass[0]),
		0,
		0,
		&ref,
	)
	if status != C.errSecSuccess {
		return nil, fmt.Errorf("SecKeychainCreate: %s", secMessage(status))
	}
	status = C.SecKeychainUnlock(ref, C.UInt32(len(pass)), unsafe.Pointer(&pass[0]), C.true)
	if status != C.errSecSuccess {
		C.SecKeychainDelete(ref)
		C.CFRelease(C.CFTypeRef(ref))
		return nil, fmt.Errorf("SecKeychainUnlock: %s", secMessage(status))
	}

	var prev C.CFArrayRef
	status = C.SecKeychainCopySearchList(&prev)
	if status != C.errSecSuccess {
		C.SecKeychainDelete(ref)
		C.CFRelease(C.CFTypeRef(ref))
		return nil, fmt.Errorf("SecKeychainCopySearchList: %s", secMessage(status))
	}
	arr := C.CFArrayCreate(C.kCFAllocatorDefault, (*unsafe.Pointer)(unsafe.Pointer(&ref)), 1, &C.kCFTypeArrayCallBacks)
	if arr == 0 {
		C.CFRelease(C.CFTypeRef(prev))
		C.SecKeychainDelete(ref)
		C.CFRelease(C.CFTypeRef(ref))
		return nil, fmt.Errorf("CFArrayCreate")
	}
	status = C.SecKeychainSetSearchList(arr)
	C.CFRelease(C.CFTypeRef(arr))
	if status != C.errSecSuccess {
		C.CFRelease(C.CFTypeRef(prev))
		C.SecKeychainDelete(ref)
		C.CFRelease(C.CFTypeRef(ref))
		return nil, fmt.Errorf("SecKeychainSetSearchList: %s", secMessage(status))
	}
	return &Keychain{scoped: true, prevSearch: prev, keychain: ref}, nil
}

func (k *Keychain) destroyFileKeychain() {
	if k == nil || !k.scoped {
		return
	}
	if k.prevSearch != 0 {
		C.SecKeychainSetSearchList(k.prevSearch)
		C.CFRelease(C.CFTypeRef(k.prevSearch))
		k.prevSearch = 0
	}
	if k.keychain != 0 {
		C.SecKeychainDelete(k.keychain)
		C.CFRelease(C.CFTypeRef(k.keychain))
		k.keychain = 0
	}
	k.scoped = false
}
