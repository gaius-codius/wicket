package rdp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gaius-codius/wicket/internal/config"
)

const stdinFlag = "/from-stdin:force"

// Plan is the argv for a FreeRDP client. It cannot carry a password.
type Plan struct {
	Client string
	Args   []string
}

// BuildPlan builds REQ-016 tokens plus the verified stdin flag. No password, no /cert:ignore.
func BuildPlan(p config.Profile) (Plan, error) {
	if err := config.ValidateProfileInUse(p); err != nil {
		return Plan{}, err
	}
	if strings.ContainsRune(p.Client, '/') || strings.ContainsAny(p.Client, " \t") {
		return Plan{}, ErrClientNotFound
	}
	args := []string{
		"/v:" + p.Host,
		"/u:" + p.User,
	}
	if p.Domain != "" {
		args = append(args, "/d:"+p.Domain)
	}
	if size := strings.TrimSpace(p.Size); size != "" {
		// A hand-edited config can hold padding that validation tolerates;
		// FreeRDP would take the spaces as part of the value.
		args = append(args, "/size:"+size)
	}
	if p.Fullscreen || p.Multimon {
		args = append(args, "/f")
	}
	if p.Multimon {
		args = append(args, "/multimon")
	}
	if p.DynamicResolution {
		args = append(args, "+dynamic-resolution")
	}
	if p.Scale == 140 || p.Scale == 180 {
		args = append(args, "/scale:"+strconv.Itoa(p.Scale))
	}
	if !p.Clipboard {
		// FreeRDP 3 shares the clipboard by default; only "-clipboard"
		// turns it off.
		args = append(args, "-clipboard")
	}
	if p.ShareHome {
		// FreeRDP finds the home folder itself, as it launches, so no path
		// is put on the command line or needs checking here.
		args = append(args, "+home-drive")
	}
	for _, s := range p.Shares {
		arg, err := driveArg(s)
		if err != nil {
			return Plan{}, err
		}
		args = append(args, arg)
	}
	args = append(args, stdinFlag)
	for _, a := range args {
		if strings.HasPrefix(a, "/p:") || strings.HasPrefix(a, "/p") && (len(a) == 2 || a[2] == ':') {
			return Plan{}, fmt.Errorf("password must not appear in argv")
		}
		if strings.Contains(a, "cert:ignore") || a == "/cert:ignore" {
			return Plan{}, fmt.Errorf("must not pass /cert:ignore")
		}
	}
	return Plan{Client: p.Client, Args: args}, nil
}

// driveArg is FreeRDP's /drive:name,path for one shared folder. The path is
// expanded here so ~ in the config becomes a real directory on argv.
func driveArg(s config.Share) (string, error) {
	path, err := config.ExpandPath(s.Path)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(s.Name)
	if name == "" {
		name = config.ShareNameFromPath(path)
	}
	return "/drive:" + name + "," + path, nil
}
