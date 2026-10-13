package install_test

// These tests run install.sh against a fake release served over TLS on
// localhost, with a PATH that holds only the tools the script needs and one
// fetcher, so the curl, GNU wget, busybox and wget2 paths are each taken.
// Nothing reaches the network or the real ~/.local/bin: HOME, TMPDIR and
// WICKET_BINDIR all point into the test's temp dir.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gaius-codius/wicket/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Sandbox(m)) }

// With WICKET_INSTALL_TESTS=all a missing fetcher fails the test instead of
// skipping it, so CI cannot pass without covering every path.
var requireAll = os.Getenv("WICKET_INSTALL_TESTS") == "all"

const (
	version = "1.2.3"
	tag     = "v" + version
	asset   = "wicket_" + version + "_linux_amd64"
)

// fetchers are the download paths install.sh can take.
var fetchers = []string{"curl", "wget", "busybox", "wget2"}

// release is a fake GitHub release site. A download it is told to redirect
// goes to /mirror/ on a plain-http twin, on a second https server (as GitHub
// sends every asset to another host), or on itself by a relative Location.
// One it is told to stall blocks until the client hangs up.
type release struct {
	tls, mirror, plain *httptest.Server
	certFile           string
	// url and mirrorURL are where tls and mirror are reached.
	url, mirrorURL string

	mu       sync.Mutex
	latest   string
	files    map[string][]byte // by path under /releases/download/
	redirect map[string]string // by the same key: "http", "https" or "relative"
	stall    string
	stalled  chan struct{} // closed once the stalled request arrives
	hungUp   chan struct{} // closed once its client has gone
	requests []string
	mirrored []string // paths the second https server was asked for
}

// newRelease starts a release site for fetcher to download from.
func newRelease(t *testing.T, fetcher string) *release {
	t.Helper()
	r := &release{
		latest:   tag,
		files:    map[string][]byte{},
		redirect: map[string]string{},
		stalled:  make(chan struct{}),
		hungUp:   make(chan struct{}),
	}
	r.addTarball(t, tag, asset, tarball(t, asset, version))
	mirror := func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.mirrored = append(r.mirrored, req.URL.Path)
		r.mu.Unlock()
		r.serve(w, req)
	}
	cert, err := testCert()
	if err != nil {
		t.Fatal(err)
	}
	r.tls = httptest.NewUnstartedServer(http.HandlerFunc(r.serve))
	r.mirror = httptest.NewUnstartedServer(http.HandlerFunc(mirror))
	for _, s := range []*httptest.Server{r.tls, r.mirror} {
		s.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
		if fetcher == "busybox-internal" {
			// Busybox's own TLS fails against Go's AES-GCM (bad record
			// MAC) and P-256 key exchange (invalid ClientKeyExchange);
			// this is the suite the two agree on.
			s.TLS = &tls.Config{
				Certificates:     []tls.Certificate{cert},
				MaxVersion:       tls.VersionTLS12,
				CurvePreferences: []tls.CurveID{tls.X25519},
				CipherSuites:     []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256},
			}
		}
		s.StartTLS()
		t.Cleanup(s.Close)
	}
	r.plain = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.plain.Close)
	r.url, r.mirrorURL = r.tls.URL, r.mirror.URL
	if fetcher == "busybox" {
		// Busybox hands openssl "-verify_ip 127.0.0.1:<port>", port and
		// all, which no certificate matches; a name is checked properly.
		r.url = strings.Replace(r.url, "127.0.0.1", "localhost", 1)
		r.mirrorURL = strings.Replace(r.mirrorURL, "127.0.0.1", "localhost", 1)
	}

	r.certFile = filepath.Join(t.TempDir(), "cert.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	if err := os.WriteFile(r.certFile, block, 0o600); err != nil {
		t.Fatal(err)
	}
	return r
}

// testCert is a self-signed certificate for localhost and 127.0.0.1, made
// once. httptest's own names no localhost, and its key is not RSA, which
// busybox's own TLS needs.
var testCert = sync.OnceValues(func() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
})

// addTarball publishes tgz as release tag's name.tar.gz, with a SHA256SUMS
// that vouches for it.
func (r *release) addTarball(t *testing.T, tag, name string, tgz []byte) {
	t.Helper()
	sum := sha256.Sum256(tgz)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[tag+"/"+name+".tar.gz"] = tgz
	r.files[tag+"/SHA256SUMS"] = fmt.Appendf(nil, "%s  %s.tar.gz\n", hex.EncodeToString(sum[:]), name)
}

func (r *release) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.requests = append(r.requests, req.URL.Path)
	latest, stall := r.latest, r.stall
	r.mu.Unlock()

	switch p := req.URL.Path; {
	case p == "/releases/latest":
		// As GitHub does: an absolute redirect to the tag's page.
		w.Header().Set("Location", r.url+"/releases/tag/"+latest)
		w.WriteHeader(http.StatusFound)
	case strings.HasPrefix(p, "/releases/tag/"):
		fmt.Fprintln(w, "release page")
	case p == "/api/releases/latest":
		fmt.Fprintf(w, "{\n  \"url\": \"x\",\n  \"tag_name\": %q,\n  \"name\": \"x\"\n}\n", latest)
	case strings.HasPrefix(p, "/mirror/"):
		r.mu.Lock()
		body, ok := r.files[strings.TrimPrefix(p, "/mirror/")]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	case strings.HasPrefix(p, "/releases/download/"):
		key := strings.TrimPrefix(p, "/releases/download/")
		r.mu.Lock()
		body, ok := r.files[key]
		redirect := r.redirect[key]
		r.mu.Unlock()
		switch {
		case key == stall:
			close(r.stalled)
			<-req.Context().Done()
			close(r.hungUp)
		case redirect == "http":
			http.Redirect(w, req, r.plain.URL+"/mirror/"+key, http.StatusFound)
		case redirect == "https":
			http.Redirect(w, req, r.mirrorURL+"/mirror/"+key, http.StatusFound)
		case redirect == "relative":
			http.Redirect(w, req, "/mirror/"+key, http.StatusFound)
		case ok:
			_, _ = w.Write(body)
		default:
			http.NotFound(w, req)
		}
	default:
		http.NotFound(w, req)
	}
}

// set changes what the server does, under its lock.
func (r *release) set(change func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	change()
}

func (r *release) downloads() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var d []string
	for _, p := range r.requests {
		if strings.HasPrefix(p, "/releases/download/") {
			d = append(d, p)
		}
	}
	return d
}

// tarball builds release tarball name, whose wicket prints
// "wicket <version>".
func tarball(t *testing.T, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	dir := name + "/"
	body := fmt.Sprintf("#!/bin/sh\necho 'wicket %s'\n", version)
	for _, h := range []*tar.Header{
		{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: dir + "wicket", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// installer is one sandbox to run install.sh in.
type installer struct {
	dir, bin, bindir, tmp string
	env                   []string
	// installSh is the install.sh to run; a test can point it at a copy.
	installSh string
	// leakTag marks every process the script starts, so the test can find
	// any left running (see testutil.StopLeakedClients).
	leakTag string
}

// tools are what install.sh and its stubs run besides the fetcher and gh.
var tools = []string{
	"awk", "cat", "chmod", "cp", "cut", "date", "env", "grep", "gzip", "head",
	"mkdir", "mktemp", "mv", "rm", "sed", "sleep", "tar",
}

func newInstaller(t *testing.T, r *release, fetcher string) *installer {
	t.Helper()
	if runtime.GOOS != "linux" {
		// The leak checks read /proc, and the fetchers under test are the
		// ones Linux distributions ship.
		t.Skip("install.sh tests run on Linux")
	}
	dir := t.TempDir()
	script, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	in := &installer{
		dir:       dir,
		bin:       filepath.Join(dir, "bin"),
		bindir:    filepath.Join(dir, "home", ".local", "bin"),
		tmp:       filepath.Join(dir, "tmp"),
		installSh: script,
		leakTag:   filepath.Join(dir, "leak"),
	}
	home := filepath.Join(dir, "home")
	for _, d := range []string{in.bin, home, in.tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range append(tools, sha256Tool(t)) {
		in.link(t, tool, mustLookPath(t, tool))
	}
	in.linkFetcher(t, fetcher)
	// Optional: without it install.sh can stop only the commands it ran
	// itself, not what they started (see kill_tree).
	if p, err := exec.LookPath("pgrep"); err == nil {
		in.link(t, "pgrep", p)
	}
	in.script(t, "uname", `case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac`)

	wgetrc := filepath.Join(home, ".wgetrc")
	if err := os.WriteFile(wgetrc, []byte("ca_certificate = "+r.certFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wget2rc := filepath.Join(home, ".wget2rc")
	if err := os.WriteFile(wget2rc, []byte("ca-certificate = "+r.certFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in.env = []string{
		"PATH=" + in.bin,
		"HOME=" + home,
		"TMPDIR=" + in.tmp,
		"WICKET_BINDIR=" + in.bindir,
		"WICKET_RELEASES_URL=" + r.url + "/releases",
		"WICKET_API_URL=" + r.url + "/api",
		"CURL_CA_BUNDLE=" + r.certFile,
		"WGETRC=" + wgetrc,
		"WGET2RC=" + wget2rc,
		"SSL_CERT_FILE=" + r.certFile,
		"FAKERDP_SANDBOX=" + in.leakTag,
	}
	// checkNothingLeft reports leaks; this stops them even when a test
	// fails before it gets there.
	t.Cleanup(func() { testutil.StopLeakedClients(in.leakTag, 0) })
	return in
}

func sha256Tool(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sha256sum"); err == nil {
		return "sha256sum"
	}
	return "shasum"
}

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("install.sh needs %s: %v", name, err)
	}
	return p
}

// needPgrep skips the test, or fails it under WICKET_INSTALL_TESTS=all,
// when there is no pgrep to give install.sh.
func needPgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pgrep"); err == nil {
		return
	}
	if requireAll {
		t.Fatal("pgrep not installed")
	}
	t.Skip("pgrep not installed")
}

// linkFetcher puts fetcher on PATH as the name install.sh looks for.
func (in *installer) linkFetcher(t *testing.T, fetcher string) {
	t.Helper()
	missing := func(why string) {
		if requireAll {
			t.Fatalf("%s: %s", fetcher, why)
		}
		t.Skipf("%s: %s", fetcher, why)
	}
	switch fetcher {
	case "curl":
		p, err := exec.LookPath("curl")
		if err != nil {
			missing("not installed")
		}
		in.link(t, "curl", p)
	case "wget":
		p, err := exec.LookPath("wget")
		if err != nil {
			missing("not installed")
		}
		if !strings.Contains(versionOf(t, p, "--version"), "GNU Wget 1.") {
			missing("wget on PATH is not GNU Wget 1.x")
		}
		in.link(t, "wget", p)
	case "busybox", "busybox-internal":
		p, err := exec.LookPath("busybox")
		if err != nil {
			missing("not installed")
		}
		// busybox picks the applet from the name it is run as.
		in.link(t, "wget", p)
		if fetcher == "busybox-internal" {
			// No openssl, so busybox falls back to its own TLS.
			break
		}
		// Busybox checks certificates only through openssl s_client,
		// which finds the test's in SSL_CERT_FILE.
		o, err := exec.LookPath("openssl")
		if err != nil {
			missing("openssl not installed")
		}
		in.link(t, "openssl", o)
	case "wget2":
		p, err := exec.LookPath("wget2")
		if err != nil {
			missing("not installed")
		}
		in.link(t, "wget", p)
	case "none":
		// The test puts a stub on PATH itself.
	default:
		t.Fatalf("unknown fetcher %q", fetcher)
	}
}

func versionOf(t *testing.T, path string, args ...string) string {
	t.Helper()
	out, _ := exec.CommandContext(t.Context(), path, args...).CombinedOutput()
	return string(out)
}

func (in *installer) link(t *testing.T, name, target string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(in.bin, name)); err != nil {
		t.Fatal(err)
	}
}

// script writes an sh script to the sandbox's PATH, in place of any tool
// linked there under that name.
func (in *installer) script(t *testing.T, name, body string) {
	t.Helper()
	p := filepath.Join(in.bin, name)
	// Not through the link, which would overwrite the real tool.
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	writeScript(t, p, body)
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (in *installer) command(ctx context.Context, t *testing.T, args ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.CommandContext(ctx, mustLookPath(t, "sh"), append([]string{in.installSh}, args...)...)
	cmd.Env = in.env
	cmd.Dir = in.dir
	// A download the script failed to stop holds its output open; Wait
	// would block on it for good.
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	return cmd, &out
}

// run runs install.sh to the end and returns its output and exit status.
func (in *installer) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd, out := in.command(ctx, t, args...)
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return out.String(), exit.ExitCode()
	default:
		t.Fatalf("install.sh: %v\n%s", err, out)
		return "", 0
	}
}

// installed is the output of the installed wicket's --version, or "".
func (in *installer) installed(t *testing.T) string {
	t.Helper()
	p := filepath.Join(in.bindir, "wicket")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return strings.TrimSpace(versionOf(t, p, "--version"))
}

// checkNothingLeft fails the test if the script left its temp dir behind or
// any process running.
func (in *installer) checkNothingLeft(t *testing.T) {
	t.Helper()
	// Only the script's mktemp -d dir: wget2 leaves lock files of its own.
	if left, _ := filepath.Glob(filepath.Join(in.tmp, "tmp.*")); len(left) > 0 {
		t.Errorf("temp dir left behind: %s", left[0])
	}
	if leaked := testutil.StopLeakedClients(in.leakTag, time.Second); len(leaked) > 0 {
		t.Errorf("left running (now killed): %s", strings.Join(leaked, "; "))
	}
}

func wantOutput(t *testing.T, out string, code, wantCode int, want string) {
	t.Helper()
	if code != wantCode || !strings.Contains(out, want) {
		t.Fatalf("exit %d, want %d with %q in the output:\n%s", code, wantCode, want, out)
	}
}

func TestInstallAndUpdate(t *testing.T) {
	t.Parallel()
	for _, fetcher := range fetchers {
		t.Run(fetcher, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, fetcher)
			in := newInstaller(t, r, fetcher)

			out, code := in.run(t)
			wantOutput(t, out, code, 0, "installed wicket "+version+" to "+in.bindir+"/wicket")
			if !strings.Contains(out, "provenance not checked; install gh") {
				t.Errorf("no note that provenance went unchecked:\n%s", out)
			}
			if got := in.installed(t); got != "wicket "+version {
				t.Fatalf("installed wicket says %q", got)
			}

			out, code = in.run(t)
			wantOutput(t, out, code, 0, "wicket "+version+" is already installed")

			writeScript(t, filepath.Join(in.bindir, "wicket"), "echo 'wicket 1.2.2'")
			out, code = in.run(t)
			wantOutput(t, out, code, 0, "updated wicket 1.2.2 -> "+version)
			if got := in.installed(t); got != "wicket "+version {
				t.Fatalf("updated wicket says %q", got)
			}
			in.checkNothingLeft(t)
		})
	}
}

func TestInstallRefusesHTTP(t *testing.T) {
	t.Parallel()
	r := newRelease(t, "curl")
	for _, v := range []string{"WICKET_RELEASES_URL", "WICKET_API_URL"} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			in := newInstaller(t, r, "curl")
			in.env = append(in.env, v+"="+r.plain.URL+"/releases")
			out, code := in.run(t)
			wantOutput(t, out, code, 1, v+" must be an https URL")
			if d := r.downloads(); len(d) > 0 {
				t.Errorf("downloaded %v", d)
			}
		})
	}
}

func TestInstallRefusesRedirectToHTTP(t *testing.T) {
	t.Parallel()
	refusal := map[string]string{
		"curl":    "download failed",
		"wget":    "refused a redirect away from https",
		"busybox": "refused a redirect away from https",
		"wget2":   "wget2 wrote nothing",
	}
	for _, fetcher := range fetchers {
		t.Run(fetcher, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, fetcher)
			r.set(func() { r.redirect[tag+"/"+asset+".tar.gz"] = "http" })
			in := newInstaller(t, r, fetcher)
			out, code := in.run(t)
			wantOutput(t, out, code, 1, refusal[fetcher])
			if got := in.installed(t); got != "" {
				t.Errorf("installed %q", got)
			}
			in.checkNothingLeft(t)
		})
	}
}

// The latest tag goes into a path and a URL, so both fetch paths take only a
// plain vX.Y.Z.
func TestInstallRefusesOddLatestTag(t *testing.T) {
	t.Parallel()
	for _, fetcher := range fetchers {
		for _, latest := range []string{
			"v1.2.3/../x", "x/v1.2.3", "v1.2.3%2F..%2Fx", "v1.2.3-rc1", "v1.2", "v1.2.3.4", "v1..2.3", "v1.2.3.", "1.2.3",
		} {
			t.Run(fetcher+"/"+latest, func(t *testing.T) {
				t.Parallel()
				r := newRelease(t, fetcher)
				r.set(func() { r.latest = latest })
				in := newInstaller(t, r, fetcher)
				out, code := in.run(t)
				wantOutput(t, out, code, 1, "no release found")
				if d := r.downloads(); len(d) > 0 {
					t.Errorf("downloaded %v", d)
				}
			})
		}
	}
}

func TestInstallProvenance(t *testing.T) {
	t.Parallel()
	const help = "      --source-ref string   ...\n"
	for _, tc := range []struct {
		name     string
		gh       string // stub body; "" leaves gh off PATH
		wantCode int
		want     string
	}{
		{"missing", "", 0, "provenance not checked; install gh"},
		{"too old", `[ "$3" = --help ] && echo '      --owner string'; exit 0`, 0, "this gh is too old"},
		{"passing", "", 0, "verified provenance: built by gaius-codius/wicket's release workflow from " + tag},
		{"failing", "", 1, "provenance check failed for " + asset + ".tar.gz"},
		{"logged out", "", 0, "gh is not logged in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, "curl")
			in := newInstaller(t, r, "curl")
			args := filepath.Join(in.dir, "gh-args")
			verify := map[string]string{"passing": "0", "failing": "1", "logged out": "4"}
			switch {
			case tc.gh != "":
				in.script(t, "gh", tc.gh)
			case verify[tc.name] != "":
				in.script(t, "gh", fmt.Sprintf(
					`[ "$3" = --help ] && { printf '%%s' '%s'; exit 0; }
printf '%%s\n' "GH_HOST=$GH_HOST" "$@" >%q
echo 'verification output'
exit %s`, help, args, verify[tc.name]))
			}
			out, code := in.run(t)
			wantOutput(t, out, code, tc.wantCode, tc.want)
			if got := in.installed(t); (got != "") != (tc.wantCode == 0) {
				t.Errorf("installed %q after exit %d", got, code)
			}
			if verify[tc.name] == "" {
				return
			}
			b, err := os.ReadFile(args)
			if err != nil {
				t.Fatalf("gh attestation verify did not run: %v", err)
			}
			for _, want := range []string{
				"GH_HOST=github.com",
				"--repo\ngaius-codius/wicket\n",
				"--cert-identity\nhttps://github.com/gaius-codius/wicket/.github/workflows/release.yml@refs/tags/" + tag + "\n",
			} {
				if !strings.Contains(string(b), want) {
					t.Errorf("gh ran without %q:\n%s", want, b)
				}
			}
		})
	}
}

// A release from before attestations is checked against the digest the
// script carries, not the SHA256SUMS next to it, and gh is not asked.
func TestInstallRefusesSubstitutedUnattestedRelease(t *testing.T) {
	t.Parallel()
	r := newRelease(t, "curl")
	const old = "wicket_0.3.1_linux_amd64"
	r.addTarball(t, "v0.3.1", old, tarball(t, old, "0.3.1"))
	in := newInstaller(t, r, "curl")
	in.script(t, "gh", "exit 0")
	out, code := in.run(t, "--version", "v0.3.1")
	wantOutput(t, out, code, 1, old+".tar.gz does not match the digest this installer has for v0.3.1")
	if got := in.installed(t); got != "" {
		t.Errorf("installed %q", got)
	}
}

// A release up to v0.3.1 installs when its tarball matches the digest the
// script carries. The real tarballs are not to hand, so this runs a copy of
// install.sh that pins the fake one's digest in place of the real one.
func TestInstallPinnedRelease(t *testing.T) {
	t.Parallel()
	const (
		old    = "wicket_0.3.1_linux_amd64"
		pinned = "7496a37a6ad73db67fd07dd0bff216bf625d3d0d920f8c971581dcfae48c01c8  " + old + ".tar.gz"
	)
	r := newRelease(t, "curl")
	tgz := tarball(t, old, "0.3.1")
	r.addTarball(t, "v0.3.1", old, tgz)
	in := newInstaller(t, r, "curl")
	in.script(t, "gh", "echo gh ran >&2; exit 1")

	b, err := os.ReadFile(in.installSh)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(pinned)) {
		t.Fatalf("install.sh does not pin %q", pinned)
	}
	sum := sha256.Sum256(tgz)
	b = bytes.Replace(b, []byte(pinned), []byte(hex.EncodeToString(sum[:])+"  "+old+".tar.gz"), 1)
	in.installSh = filepath.Join(in.dir, "install.sh")
	if err := os.WriteFile(in.installSh, b, 0o600); err != nil {
		t.Fatal(err)
	}

	out, code := in.run(t, "--version", "v0.3.1")
	wantOutput(t, out, code, 0, "checked against the digest this installer has for v0.3.1")
	if strings.Contains(out, "gh ran") {
		t.Errorf("gh was asked about a pinned release:\n%s", out)
	}
	if got := in.installed(t); got != "wicket 0.3.1" {
		t.Errorf("installed wicket says %q", got)
	}
}

// A platform an old release never had a build for is refused, even when the
// release site offers one.
func TestInstallRefusesUnpinnedOldAsset(t *testing.T) {
	t.Parallel()
	const old = "wicket_0.1.0_darwin_arm64"
	r := newRelease(t, "curl")
	r.addTarball(t, "v0.1.0", old, tarball(t, old, "0.1.0"))
	in := newInstaller(t, r, "curl")
	in.script(t, "uname", `case "$1" in -s) echo Darwin ;; -m) echo arm64 ;; esac`)
	out, code := in.run(t, "--version", "v0.1.0")
	wantOutput(t, out, code, 1, old+".tar.gz was never published for v0.1.0")
	if got := in.installed(t); got != "" {
		t.Errorf("installed %q", got)
	}
}

// GitHub answers every asset download with a redirect to another https host,
// so each fetcher has to follow one, and a relative one, which keeps the
// scheme.
func TestInstallFollowsHTTPSRedirect(t *testing.T) {
	t.Parallel()
	for _, fetcher := range fetchers {
		for _, kind := range []string{"https", "relative"} {
			t.Run(fetcher+"/"+kind, func(t *testing.T) {
				t.Parallel()
				r := newRelease(t, fetcher)
				r.set(func() {
					r.redirect[tag+"/"+asset+".tar.gz"] = kind
					r.redirect[tag+"/SHA256SUMS"] = kind
				})
				in := newInstaller(t, r, fetcher)
				out, code := in.run(t)
				wantOutput(t, out, code, 0, "installed wicket "+version)
				r.mu.Lock()
				mirrored, requests := r.mirrored, r.requests
				r.mu.Unlock()
				if want := map[string]int{"https": 2, "relative": 0}[kind]; len(mirrored) != want {
					t.Errorf("the second server was asked for %v, want %d downloads", mirrored, want)
				}
				var followed int
				for _, p := range requests {
					if strings.HasPrefix(p, "/mirror/") {
						followed++
					}
				}
				if followed != 2 {
					t.Errorf("followed %d redirects, want 2: %v", followed, requests)
				}
				in.checkNothingLeft(t)
			})
		}
	}
}

// Busybox without openssl falls back to TLS of its own, which accepts any
// certificate, so the download could come from anyone.
func TestInstallRefusesUncheckedTLS(t *testing.T) {
	t.Parallel()
	r := newRelease(t, "busybox-internal")
	in := newInstaller(t, r, "busybox-internal")
	out, code := in.run(t)
	wantOutput(t, out, code, 1, "does not check TLS certificates; install curl, or openssl for busybox to use")
	if got := in.installed(t); got != "" {
		t.Errorf("installed %q", got)
	}
	in.checkNothingLeft(t)
}

// Busybox before 1.33 hands https to openssl without asking it to check the
// certificate, and says nothing of it, so its version is what gives it away.
func TestInstallRefusesOldBusybox(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ banner, want string }{
		{"BusyBox v1.30.1 (Debian 1:1.30.1-6+b3) multi-call binary.", "busybox wget 1.30 does not check"},
		{"BusyBox v1.32.0 (2020-06-26 12:00:00 UTC) multi-call binary.", "busybox wget 1.32 does not check"},
		{"BusyBox multi-call binary.", "busybox wget of unknown version does not check"},
	} {
		t.Run(tc.banner, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, "curl")
			in := newInstaller(t, r, "none")
			// It would download anything, with a log that passes.
			in.script(t, "wget", fmt.Sprintf(`[ "$1" = --version ] && { echo %q >&2; exit 1; }
while [ "$1" != -O ]; do shift; done
printf x >"$2"
echo '  HTTP/1.1 200 OK' >&2`, tc.banner))
			out, code := in.run(t, "--version", tag)
			wantOutput(t, out, code, 1, tc.want)
			if got := in.installed(t); got != "" {
				t.Errorf("installed %q", got)
			}
		})
	}
}

// A wget install.sh does not know is not trusted to check certificates.
func TestInstallRefusesUnknownWget(t *testing.T) {
	t.Parallel()
	r := newRelease(t, "curl")
	in := newInstaller(t, r, "none")
	in.script(t, "wget", `[ "$1" = --version ] && { echo 'Toybox 0.8.11 wget'; exit 0; }
while [ "$1" != -O ]; do shift; done
printf x >"$2"
echo '  HTTP/1.1 200 OK' >&2`)
	out, code := in.run(t, "--version", tag)
	wantOutput(t, out, code, 1, "is not GNU Wget, Wget2 or busybox")
	if got := in.installed(t); got != "" {
		t.Errorf("installed %q", got)
	}
}

// Busybox sends an https URL to the proxy http_proxy names as a plain GET,
// so whoever runs the proxy, or sits on the way to it, could serve anything.
func TestInstallRefusesBusyboxThroughProxy(t *testing.T) {
	t.Parallel()
	r := newRelease(t, "busybox")
	in := newInstaller(t, r, "busybox")
	in.env = append(in.env, "http_proxy="+r.plain.URL)
	out, code := in.run(t)
	wantOutput(t, out, code, 1, "busybox wget sends https through http_proxy unencrypted")
	if got := in.installed(t); got != "" {
		t.Errorf("installed %q", got)
	}
	in.checkNothingLeft(t)
}

// A user's config that turns the certificate check off does not apply: with
// no CA that knows the test's certificate, nothing installs.
func TestInstallIgnoresInsecureConfig(t *testing.T) {
	t.Parallel()
	for _, fetcher := range []string{"curl", "wget", "wget2"} {
		t.Run(fetcher, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, fetcher)
			in := newInstaller(t, r, fetcher)
			home := filepath.Join(in.dir, "home")
			wgetrc := filepath.Join(home, "insecure.wgetrc")
			wget2rc := filepath.Join(home, "insecure.wget2rc")
			for p, body := range map[string]string{
				filepath.Join(home, ".curlrc"): "insecure\n",
				wgetrc:                         "check_certificate = off\n",
				wget2rc:                        "check-certificate = off\n",
			} {
				if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var env []string
			for _, e := range in.env {
				switch name, _, _ := strings.Cut(e, "="); name {
				case "CURL_CA_BUNDLE", "SSL_CERT_FILE", "WGETRC", "WGET2RC":
				default:
					env = append(env, e)
				}
			}
			env = append(env, "WGETRC="+wgetrc, "WGET2RC="+wget2rc)
			in.env = env
			// The latest tag's lookup, then (named) the download itself.
			for _, args := range [][]string{nil, {"--version", tag}} {
				out, code := in.run(t, args...)
				if code == 0 {
					t.Fatalf("installed with certificate checks off (%v):\n%s", args, out)
				}
				// Refused at the connection, not for some other reason.
				if !strings.Contains(out, "could not reach") && !strings.Contains(out, "no release found") &&
					!strings.Contains(out, "download failed") {
					t.Errorf("exit %d (%v) for another reason:\n%s", code, args, out)
				}
			}
			if got := in.installed(t); got != "" {
				t.Errorf("installed %q", got)
			}
			in.checkNothingLeft(t)
		})
	}
}

// wget_https lets nothing through that it cannot read: a log with no status
// line, or a Location wget printed in a form the check does not parse.
func TestInstallRefusesUnreadableWgetLog(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, log, want string }{
		{"no status line", "", "wget did not show its redirects"},
		{
			"escaped tab",
			`  HTTP/1.1 302 Found\n  Location: \\011http://example.com/x\n  HTTP/1.1 200 OK\n`,
			"refused a redirect away from https",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, "curl")
			in := newInstaller(t, r, "none")
			// GNU wget 1.x as far as install.sh can tell: it answers
			// --version and writes the download, with log on stderr.
			in.script(t, "wget", fmt.Sprintf(`[ "$1" = --version ] && { echo 'GNU Wget 1.21.4'; exit 0; }
while [ "$1" != -O ]; do shift; done
printf x >"$2"
printf '%s' >&2`, tc.log))
			out, code := in.run(t, "--version", tag)
			wantOutput(t, out, code, 1, tc.want)
			if got := in.installed(t); got != "" {
				t.Errorf("installed %q", got)
			}
		})
	}
}

// The installer warns when no client on PATH is FreeRDP 3, reading the
// version an unsuffixed name prints whatever its exit status.
func TestInstallFreeRDP3Warning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, client string
		warn         bool
	}{
		{"2.11.7", "echo 'This is FreeRDP version 2.11.7 (n/a)'", true},
		{"3.31.1", "echo 'This is FreeRDP version 3.31.1 (n/a)'", false},
		{"3.31.1 then exit 1", "echo 'This is FreeRDP version 3.31.1 (n/a)'; exit 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, "curl")
			in := newInstaller(t, r, "curl")
			in.script(t, "xfreerdp", tc.client)
			out, code := in.run(t)
			wantOutput(t, out, code, 0, "installed wicket "+version)
			if got := strings.Contains(out, "warning: no FreeRDP 3 client on PATH"); got != tc.warn {
				t.Errorf("warned=%v, want %v:\n%s", got, tc.warn, out)
			}
			in.checkNothingLeft(t)
		})
	}
}

// A signal during a stalled download stops the script with the shell's
// status for it, and takes the download and the temp dir with it. Ctrl-C
// in a terminal goes to the whole process group, the download included.
func TestInstallStopsOnSignal(t *testing.T) {
	t.Parallel()
	for _, fetcher := range fetchers {
		for _, sig := range []signal{
			{sig: syscall.SIGHUP, code: 129},
			{sig: syscall.SIGINT, code: 130},
			{sig: syscall.SIGINT, code: 130, group: true},
			{sig: syscall.SIGQUIT, code: 131},
			{sig: syscall.SIGQUIT, code: 131, group: true},
			{sig: syscall.SIGTERM, code: 143},
		} {
			t.Run(fmt.Sprintf("%s/%v/group=%v", fetcher, sig.sig, sig.group), func(t *testing.T) {
				t.Parallel()
				if fetcher == "busybox" {
					// Its https helper outlives a killed wget.
					needPgrep(t)
				}
				r := newRelease(t, fetcher)
				r.set(func() { r.stall = tag + "/" + asset + ".tar.gz" })
				in := newInstaller(t, r, fetcher)
				in.signalDuring(t, r.stalled, sig)
				select {
				case <-r.hungUp:
				case <-time.After(2 * time.Second):
					t.Error("the download was still connected after the script stopped")
				}
				in.checkNothingLeft(t)
			})
		}
	}
}

// A TERM while the installed wicket's --version hangs stops the script at
// once rather than when that returns. One that ignores TERM is killed after
// a grace period, and so is one that a wrapper started, which TERM leaves
// behind when the wrapper goes.
func TestInstallStopsWhileVersionCheckHangs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		body   string // %q is the file to create once it runs
		within time.Duration
	}{
		{"hangs", ": >%q\nexec sleep 30", time.Second},
		// A 2 s grace period, and slack for a loaded machine.
		{"ignores TERM", "trap '' TERM\n: >%q\nexec sleep 30", 5 * time.Second},
		{"wrapper of one that ignores TERM", "/bin/sh -c \"trap '' TERM; : >%q; exec sleep 30\" &\nwait", 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			needPgrep(t)
			r := newRelease(t, "curl")
			in := newInstaller(t, r, "curl")
			started := filepath.Join(in.dir, "version-started")
			writeScript(t, filepath.Join(in.bindir, "wicket"), fmt.Sprintf(tc.body, started))
			in.signalDuring(t, appears(t, started), signal{sig: syscall.SIGTERM, code: 143, within: tc.within})
			in.checkNothingLeft(t)
		})
	}
}

// A signal while the new binary is copied into place takes the copy with it,
// and leaves the old wicket.
func TestInstallStopsDuringCopy(t *testing.T) {
	t.Parallel()
	r := newRelease(t, "curl")
	in := newInstaller(t, r, "curl")
	started := filepath.Join(in.dir, "copy-started")
	in.script(t, "cp", fmt.Sprintf("%q \"$@\"\n: >%q\nexec sleep 30", mustLookPath(t, "cp"), started))
	in.signalDuring(t, appears(t, started), signal{sig: syscall.SIGTERM, code: 143})
	if left, _ := filepath.Glob(filepath.Join(in.bindir, ".wicket*")); len(left) > 0 {
		t.Errorf("left in %s: %v", in.bindir, left)
	}
	if got := in.installed(t); got != "" {
		t.Errorf("installed %q", got)
	}
	in.checkNothingLeft(t)
}

// A FreeRDP client that is a wrapper and hangs is stopped with what it
// started, both when the probe gives up on it and when the script is
// stopped during the probe.
func TestInstallStopsHungFreeRDPWrapper(t *testing.T) {
	t.Parallel()
	needPgrep(t)
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("signalled=%v", stop), func(t *testing.T) {
			t.Parallel()
			r := newRelease(t, "curl")
			in := newInstaller(t, r, "curl")
			started := filepath.Join(in.dir, "client-started")
			// Not exec: the wrapper forks the "real" client and waits.
			in.script(t, "xfreerdp", fmt.Sprintf(": >%q\nsleep 30\nexit 0", started))
			if stop {
				in.signalDuring(t, appears(t, started), signal{sig: syscall.SIGTERM, code: 143})
			} else {
				out, code := in.run(t)
				wantOutput(t, out, code, 0, "warning: no FreeRDP 3 client on PATH")
			}
			in.checkNothingLeft(t)
		})
	}
}

// appears is closed once path exists.
func appears(t *testing.T, path string) <-chan struct{} {
	t.Helper()
	ready := make(chan struct{})
	go func() {
		for t.Context().Err() == nil {
			if _, err := os.Stat(path); err == nil {
				close(ready)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return ready
}

// signal is what signalDuring sends and what it wants back.
type signal struct {
	sig  syscall.Signal
	code int
	// group sends sig to install.sh's process group, as a terminal does.
	group bool
	// within is how long install.sh has to exit; zero means a second.
	within time.Duration
}

// signalDuring starts install.sh, sends s.sig once ready is closed, and
// wants it to exit with s.code in time.
func (in *installer) signalDuring(t *testing.T, ready <-chan struct{}, s signal) {
	t.Helper()
	within := s.within
	if within == 0 {
		within = time.Second
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, out := in.command(ctx, t)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: s.group}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("install.sh exited early: %v\n%s", err, out)
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		t.Fatalf("install.sh never reached the point to signal it:\n%s", out)
	}
	pid := cmd.Process.Pid
	if s.group {
		pid = -pid
	}
	if err := syscall.Kill(pid, s.sig); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != s.code {
			t.Errorf("after %v: %v, want exit status %d\n%s", s.sig, err, s.code, out)
		}
	case <-time.After(within):
		cancel()
		<-done
		t.Errorf("install.sh still running %v after %v\n%s", within, s.sig, out)
	}
}
