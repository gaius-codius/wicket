package rdp

import (
	"path/filepath"

	"github.com/gaius-codius/wicket/internal/config"
)

// FreeRDP's clients exit with a code that says why the session ended, and
// most of the non-zero ones are not failures: a remote logoff, an idle
// timeout, or another session taking over all exit non-zero, and the SDL
// client exits 1 when its window is simply closed. Reading every non-zero
// exit as an error put a user who signed out of Windows on the retry view,
// with a hint that their password might be wrong.
//
// The codes are FreeRDP 3's, from the SDL client's EXIT_CODE enum
// (client/SDL/SDL3/sdl_utils.hpp) and the X11 client's XF_EXIT_CODE
// (client/X11/xfreerdp.h), which give every value the same number. The X11
// client also passes errinfo 12, a logoff by the user, through as it is.
// They are applied only to those clients: a profile's client can be anything,
// and another program's exit codes mean something else.

// freerdpClients are the basenames whose exit codes follow the table.
var freerdpClients = map[string]bool{
	ClientSDL:     true,
	"sdl-freerdp": true,
	ClientX11:     true,
	"xfreerdp":    true,
}

// The FreeRDP 3 clients Wicket offers by name.
const (
	ClientSDL = "sdl-freerdp3"
	ClientX11 = "xfreerdp3"
)

// KnownClient is a FreeRDP client the profile form offers by name.
type KnownClient struct {
	Name string
	// About says in a few words what the client is, for the form's help.
	About string
}

// KnownClients are the clients the form offers, most preferred first; a new
// profile gets the first one installed. wlfreerdp3 is left out for now: how
// well it copes with Wicket's options is still being looked into.
var KnownClients = []KnownClient{
	{ClientSDL, "FreeRDP's SDL client (native Wayland and X11)"},
	{ClientX11, "FreeRDP's X11 client (runs through XWayland on Wayland)"},
}

// InstalledClients returns the known clients lookPath finds, in order of
// preference. lookPath is exec.LookPath in production; tests pass their own
// so that nothing depends on what the machine has installed.
func InstalledClients(lookPath func(string) (string, error)) []KnownClient {
	var out []KnownClient
	for _, c := range KnownClients {
		if _, err := lookPath(c.Name); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// PreferredClient is the client a new profile starts with: the first known
// client installed, or config.DefaultClient when none is.
func PreferredClient(installed []KnownClient) string {
	if len(installed) > 0 {
		return installed[0].Name
	}
	return config.DefaultClient
}

// AboutClient describes name when it is a known client.
func AboutClient(name string) (string, bool) {
	for _, c := range KnownClients {
		if c.Name == name {
			return c.About, true
		}
	}
	return "", false
}

// exitMeaning is what one FreeRDP exit code says.
type exitMeaning struct {
	// reason is a few words for the status line; empty for a plain end.
	reason string
	// ended marks an ending the user or the server chose, not a failure.
	ended bool
	// credentials marks a failure the password may be to blame for.
	credentials bool
}

var freerdpExits = map[int]exitMeaning{
	0: {ended: true},
	// The SDL client exits 1 both for DISCONNECT and when its window is
	// closed at the end of an ordinary session.
	1:  {reason: "disconnected", ended: true},
	2:  {reason: "logged off", ended: true},
	3:  {reason: "idle timeout", ended: true},
	4:  {reason: "logon timeout", ended: true},
	5:  {reason: "another session took over", ended: true},
	6:  {reason: "out of memory"},
	7:  {reason: "the server denied the connection"},
	8:  {reason: "the server denied the connection (FIPS)"},
	9:  {reason: "insufficient privileges"},
	10: {reason: "the server wants fresh credentials", credentials: true},
	11: {reason: "disconnected by the user", ended: true},
	12: {reason: "logged off", ended: true},
	16: {reason: "licensing failed"},
	17: {reason: "no license server"},
	18: {reason: "no license available"},
	19: {reason: "licensing failed"},
	20: {reason: "licensing failed"},
	21: {reason: "licensing failed"},
	22: {reason: "licensing failed"},
	23: {reason: "licensing failed"},
	24: {reason: "licensing failed"},
	25: {reason: "licensing failed"},
	26: {reason: "the server allows no remote connections"},
	32: {reason: "RDP protocol error"},

	128: {reason: "invalid arguments"},
	129: {reason: "out of memory"},
	130: {reason: "protocol error"},
	131: {reason: "connection failed"},
	132: {reason: "authentication failed", credentials: true},
	133: {reason: "security negotiation failed"},
	134: {reason: "logon failed", credentials: true},
	135: {reason: "account locked out"},
	136: {reason: "could not start connecting"},
	137: {reason: "connection failed"},
	138: {reason: "connection failed after logon"},
	139: {reason: "DNS error"},
	140: {reason: "host name not found"},
	141: {reason: "could not connect"},
	142: {reason: "connection failed"},
	143: {reason: "TLS connection failed"},
	144: {reason: "insufficient privileges"},
	145: {reason: "connection cancelled", ended: true},
	147: {reason: "could not connect"},
	148: {reason: "password expired"},
	149: {reason: "password must be changed"},
	150: {reason: "Kerberos KDC unreachable"},
	151: {reason: "account disabled"},
	152: {reason: "password expired"},
	153: {reason: "client revoked"},
	154: {reason: "wrong password", credentials: true},
	155: {reason: "access denied"},
	156: {reason: "account restriction"},
	157: {reason: "account expired"},
	158: {reason: "logon type not granted"},
	159: {reason: "no credentials", credentials: true},
	160: {reason: "the host is still starting"},
	161: {reason: "the server requires NLA"},
}

// IsFreeRDP reports whether client, a basename, is one of FreeRDP's own
// clients, whose exit codes Wicket knows.
func IsFreeRDP(client string) bool {
	return freerdpClients[filepath.Base(client)]
}

// exitPreConnectFailed is ERRCONNECT_PRE_CONNECT_FAILED: the client gave up
// before it started connecting, which is about the client and its
// surroundings rather than the host.
const exitPreConnectFailed = 136

// PreConnectFailed reports whether a FreeRDP client gave up before it began
// to connect. The SDL client does this when it cannot make sense of the
// monitors, which a fractionally scaled Wayland output can cause under /f.
func (o Outcome) PreConnectFailed() bool {
	_, ok := o.meaning()
	return ok && o.ExitCode == exitPreConnectFailed
}

// meaning looks up o's exit code, when the client is FreeRDP's and the code
// one it documents.
func (o Outcome) meaning() (exitMeaning, bool) {
	if o.StartErr != nil || o.Signaled || !IsFreeRDP(o.Client) {
		return exitMeaning{}, false
	}
	e, ok := freerdpExits[o.ExitCode]
	return e, ok
}

// Reason says in a few words why the session ended, from the client's exit
// code, or "" when the code says nothing Wicket knows.
func (o Outcome) Reason() string {
	e, _ := o.meaning()
	return e.reason
}

// MaybeCredentials reports whether the password may be why the session ended:
// the client said authentication failed, or the exit said nothing either way.
// A FreeRDP client that says it could not reach the host, or that the user
// logged off, has ruled the password out.
func (o Outcome) MaybeCredentials() bool {
	if o.StartErr != nil {
		return false
	}
	e, ok := o.meaning()
	return !ok || e.credentials
}
