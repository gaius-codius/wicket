package rdp

import "path/filepath"

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
	"sdl-freerdp3": true,
	"sdl-freerdp":  true,
	"xfreerdp3":    true,
	"xfreerdp":     true,
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
