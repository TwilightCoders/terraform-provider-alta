package device

import (
	"bytes"
	"embed"
	"strings"
	"text/template"
)

//go:embed scripts/*.sh
var scriptFS embed.FS

var scripts = template.Must(template.New("").Funcs(template.FuncMap{"q": shellQuote}).ParseFS(scriptFS, "scripts/*.sh"))

// Layout is where a router keeps its configuration and how its cloud agent is driven.
// Everything router-specific lives here, so the scripts can be exercised anywhere.
type Layout struct {
	// Dir holds the transaction journal, snapshot and rollback script. Must be persistent.
	Dir        string
	ConfigFile string
	HashFile   string
	// Shell commands.
	AgentStart   string
	AgentStop    string
	AgentRunning string
	Apply        string
	Log          string
	// HookDir holds provider-managed hooks; HotplugDir is where interface hooks are
	// installed for the current boot; PostCfg runs after every boot and configuration push.
	HookDir    string
	HotplugDir string
	PostCfg    string
	// StatMode prints a file's permission bits in octal.
	StatMode string
	// Detach starts the rollback timer in its own session so it outlives the SSH session.
	Detach string
}

// Route10Layout is the layout of an Alta Route10: /cfg is the only persistent filesystem
// and `rc` is the cloud agent.
func Route10Layout() Layout {
	return Layout{
		Dir:          "/cfg/.tf",
		ConfigFile:   "/cfg/config.json",
		HashFile:     "/cfg/hash.txt",
		AgentStart:   "/etc/init.d/rc start",
		AgentStop:    "/etc/init.d/rc stop",
		AgentRunning: "pidof rc >/dev/null",
		Apply:        "cfg",
		Log:          "logger -t alta-tf",
		Detach:       "setsid",
		HookDir:      "/cfg/tf.d",
		HotplugDir:   "/etc/hotplug.d/iface",
		PostCfg:      "/cfg/post-cfg.sh",
		StatMode:     "stat -c %a",
	}
}

type scriptData struct {
	Layout
	WindowSeconds      int
	PushTimeoutSeconds int
	ProbeSpec
}

func render(name string, data any) string {
	var buf bytes.Buffer
	if err := scripts.ExecuteTemplate(&buf, name, data); err != nil {
		panic("device: rendering " + name + ": " + err.Error())
	}
	return buf.String()
}

// shellQuote quotes s for POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
