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
	"testing"
	"time"
	"unsafe"
)

// Keychain stores secrets in the macOS keychain as generic passwords.
//
// Identity maps onto kSecAttrService + kSecAttrAccount. The config path is
// folded into the account attribute so two config files cannot share a
// secret (same guarantee as the libsecret attribute set).
type Keychain struct {
	// keychain, when non-zero, pins SecItemAdd via kSecUseKeychain.
	// When matchList is zero, queries also use a one-element MatchSearchList
	// built from this ref (file-keychain tests; never touches the user search list).
	keychain C.SecKeychainRef
	// matchList, when non-zero, is used as kSecMatchSearchList for queries
	// (dual-keychain tests). Owns a CFArray retain.
	matchList C.CFArrayRef
}

// NewKeychain returns a store that talks to the default keychain search list.
func NewKeychain() *Keychain { return &Keychain{} }

// lookupBetweenSteps, when non-nil, runs after Lookup collects attribute hits
// and before the password fetch. Tests use it to cancel mid-Lookup.
var lookupBetweenSteps func()

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

	result, status, err := secCopyMatching(ctx, C.CFDictionaryRef(query))
	if err != nil {
		return NotSaved, err
	}
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

	result, status, err := secCopyMatching(ctx, C.CFDictionaryRef(query))
	if err != nil {
		return LookupResult{}, err
	}
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

	if lookupBetweenSteps != nil {
		lookupBetweenSteps()
	}
	// Do not start the password fetch if the caller already gave up.
	if err := ctx.Err(); err != nil {
		return LookupResult{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

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
	if err := ctx.Err(); err != nil {
		return Password{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	query := C.CFDictionaryCreateMutable(C.kCFAllocatorDefault, 0, &C.kCFTypeDictionaryKeyCallBacks, &C.kCFTypeDictionaryValueCallBacks)
	if query == 0 {
		return Password{}, fmt.Errorf("%w: CFDictionaryCreateMutable", ErrUnavailable)
	}
	defer C.CFRelease(C.CFTypeRef(query))
	C.dictSet(query, C.CFTypeRef(C.kSecClass), C.CFTypeRef(C.kSecClassGenericPassword))
	C.dictSet(query, C.CFTypeRef(C.kSecValuePersistentRef), C.CFTypeRef(pref))
	C.dictSet(query, C.CFTypeRef(C.kSecMatchLimit), C.CFTypeRef(C.matchLimitOne()))
	C.dictSet(query, C.CFTypeRef(C.kSecReturnData), C.CFTypeRef(C.kCFBooleanTrue))

	result, status, err := secCopyMatching(ctx, C.CFDictionaryRef(query))
	if err != nil {
		return Password{}, err
	}
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

	status, err := secItemUpdate(ctx, C.CFDictionaryRef(query), C.CFDictionaryRef(attrs))
	if err != nil {
		return err
	}
	if status == C.errSecSuccess {
		return mapSecStatus(ctx, status)
	}
	if status != C.errSecItemNotFound {
		return mapSecStatus(ctx, status)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	add := k.baseQuery(id)
	defer C.CFRelease(C.CFTypeRef(add))
	// File-keychain tests: SecItemAdd without kSecUseKeychain can land in the
	// data-protection keychain even when MatchSearchList is scoped.
	if k.keychain != 0 {
		C.dictSet(add, C.CFTypeRef(C.kSecUseKeychain), C.CFTypeRef(k.keychain))
	}
	label := cfString(id.Label())
	defer C.CFRelease(C.CFTypeRef(label))
	C.dictSet(add, C.CFTypeRef(C.kSecAttrLabel), C.CFTypeRef(label))
	C.dictSet(add, C.CFTypeRef(C.kSecValueData), C.CFTypeRef(data))
	st, err := secItemAdd(ctx, C.CFDictionaryRef(add))
	if err != nil {
		return err
	}
	return mapSecStatus(ctx, st)
}

func (k *Keychain) Delete(ctx context.Context, id Identity) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	query := k.baseQuery(id)
	defer C.CFRelease(C.CFTypeRef(query))
	// File-keychain SecItemDelete defaults to one match without MatchLimitAll.
	C.dictSet(query, C.CFTypeRef(C.kSecMatchLimit), C.CFTypeRef(C.matchLimitAll()))
	status, err := secItemDelete(ctx, C.CFDictionaryRef(query))
	if err != nil {
		return err
	}
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

	// File-keychain tests: pin match/update/delete to the throwaway keychain(s)
	// so Upsert does not update a same-identity item in another search-list
	// keychain (and so Presence/Lookup stay isolated). SecItemAdd still needs
	// kSecUseKeychain separately — without it, adds can land in the
	// data-protection keychain. Never call SecKeychainSetSearchList: that
	// API persists to user preferences.
	if k.matchList != 0 {
		C.dictSet(query, C.CFTypeRef(C.kSecMatchSearchList), C.CFTypeRef(k.matchList))
	} else if k.keychain != 0 {
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
	// Prefer cancellation over a late native success so a cancelled caller
	// never accepts a password or treats a write as committed.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, ctxErr)
	}
	if status == C.errSecSuccess {
		return nil
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

// secCopyMatching runs SecItemCopyMatching on a worker. If ctx is cancelled
// while the call is blocked (e.g. unlock dialog), return immediately without
// waiting — the worker releases any result. Wicket cannot dismiss Keychain UI.
func secCopyMatching(ctx context.Context, query C.CFDictionaryRef) (C.CFTypeRef, C.OSStatus, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	type out struct {
		result C.CFTypeRef
		status C.OSStatus
	}
	ch := make(chan out, 1)
	go func() {
		var result C.CFTypeRef
		status := C.SecItemCopyMatching(query, &result)
		ch <- out{result, status}
	}()
	select {
	case <-ctx.Done():
		go func() {
			o := <-ch
			if o.result != 0 {
				C.CFRelease(o.result)
			}
		}()
		return 0, 0, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	case o := <-ch:
		if err := ctx.Err(); err != nil {
			if o.result != 0 {
				C.CFRelease(o.result)
			}
			return 0, o.status, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return o.result, o.status, nil
	}
}

func secItemUpdate(ctx context.Context, query, attrs C.CFDictionaryRef) (C.OSStatus, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	ch := make(chan C.OSStatus, 1)
	go func() { ch <- C.SecItemUpdate(query, attrs) }()
	select {
	case <-ctx.Done():
		go func() { <-ch }()
		return 0, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	case st := <-ch:
		if err := ctx.Err(); err != nil {
			return st, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return st, nil
	}
}

func secItemAdd(ctx context.Context, attrs C.CFDictionaryRef) (C.OSStatus, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	ch := make(chan C.OSStatus, 1)
	go func() { ch <- C.SecItemAdd(attrs, nil) }()
	select {
	case <-ctx.Done():
		go func() { <-ch }()
		return 0, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	case st := <-ch:
		if err := ctx.Err(); err != nil {
			return st, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return st, nil
	}
}

func secItemDelete(ctx context.Context, query C.CFDictionaryRef) (C.OSStatus, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	ch := make(chan C.OSStatus, 1)
	go func() { ch <- C.SecItemDelete(query) }()
	select {
	case <-ctx.Done():
		go func() { <-ch }()
		return 0, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	case st := <-ch:
		if err := ctx.Err(); err != nil {
			return st, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return st, nil
	}
}

// copySearchList returns the user's current keychain search list. Caller
// must CFRelease. Used by tests to assert fixtures never mutate prefs.
func copySearchList() (C.CFArrayRef, error) {
	var list C.CFArrayRef
	status := C.SecKeychainCopySearchList(&list)
	if status != C.errSecSuccess {
		return 0, fmt.Errorf("SecKeychainCopySearchList: %s", secMessage(status))
	}
	return list, nil
}

func searchListsEqual(a, b C.CFArrayRef) bool {
	if a == 0 || b == 0 {
		return a == b
	}
	return C.CFEqual(C.CFTypeRef(a), C.CFTypeRef(b)) != 0
}

// createUnlockedKeychainFile creates an unlocked keychain at path without
// changing the user's search list. Caller must SecKeychainDelete + CFRelease.
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

// newMultiFileKeychain creates two unlocked file keychains and a lookup store
// whose MatchSearchList spans both. older/newer Upsert into each via
// kSecUseKeychain. Does not call SecKeychainSetSearchList.
// cleanup deletes both files and releases the match array.
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
	arr := C.makeKeychainArray2(refOlder, refNewer)
	if arr == 0 {
		C.SecKeychainDelete(refOlder)
		C.CFRelease(C.CFTypeRef(refOlder))
		C.SecKeychainDelete(refNewer)
		C.CFRelease(C.CFTypeRef(refNewer))
		return nil, nil, nil, nil, fmt.Errorf("CFArrayCreate")
	}
	lookup = &Keychain{matchList: arr}
	older = &Keychain{keychain: refOlder}
	newer = &Keychain{keychain: refNewer}
	cleanup = func() {
		if lookup.matchList != 0 {
			C.CFRelease(C.CFTypeRef(lookup.matchList))
			lookup.matchList = 0
		}
		C.SecKeychainDelete(refOlder)
		C.CFRelease(C.CFTypeRef(refOlder))
		C.SecKeychainDelete(refNewer)
		C.CFRelease(C.CFTypeRef(refNewer))
		older.keychain = 0
		newer.keychain = 0
	}
	return lookup, older, newer, cleanup, nil
}

// newFileKeychain creates an unlocked keychain file scoped via
// kSecMatchSearchList / kSecUseKeychain only. Does not change the user's
// search list. Caller must call destroyFileKeychain when finished.
func newFileKeychain(path string, pass []byte) (*Keychain, error) {
	ref, err := createUnlockedKeychainFile(path, pass)
	if err != nil {
		return nil, err
	}
	return &Keychain{keychain: ref}, nil
}

func (k *Keychain) destroyFileKeychain() {
	if k == nil {
		return
	}
	if k.matchList != 0 {
		C.CFRelease(C.CFTypeRef(k.matchList))
		k.matchList = 0
	}
	if k.keychain != 0 {
		C.SecKeychainDelete(k.keychain)
		C.CFRelease(C.CFTypeRef(k.keychain))
		k.keychain = 0
	}
}

// trackSearchList snapshots the user's Keychain search list and asserts it
// is unchanged when the test finishes (including failure paths).
func trackSearchList(t *testing.T) {
	t.Helper()
	before, err := copySearchList()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := copySearchList()
		if err != nil {
			C.CFRelease(C.CFTypeRef(before))
			t.Errorf("copySearchList after test: %v", err)
			return
		}
		same := searchListsEqual(before, after)
		C.CFRelease(C.CFTypeRef(after))
		C.CFRelease(C.CFTypeRef(before))
		if !same {
			t.Errorf("user keychain search list changed during the test")
		}
	})
}
