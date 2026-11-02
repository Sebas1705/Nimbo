package nimbo

import (
	"strconv"
	"strings"
)

// Config is how Nimbo was started. It is read from the environment once and then passed around.
type Config struct {
	Port int
	// APIToken protects /api when set (except /api/health). Empty means no token.
	APIToken string
	// HostEnv is the process environment: the first place providers read their secrets from.
	HostEnv map[string]string
	// AllowedHosts are extra Host header values accepted besides loopback (e.g. behind a reverse proxy you control),
	// lowercase and without port.
	AllowedHosts []string
	// Host is the address to listen on. Anything but loopback is only sensible inside a container whose port you
	// publish on loopback.
	Host string
	// EmulatorHost is where the emulators answer from Nimbo's point of view (host.docker.internal when Nimbo itself
	// runs in Docker).
	EmulatorHost string
	// Version is empty in a development run (the health endpoint then says null instead of making one up).
	Version string
	// Distribution is how Nimbo was installed: "binary" (nimbo serve), "desktop" (Nimbo.exe) or "docker". The updater acts accordingly.
	Distribution string
	// DataDir is the folder with settings, progress and statistics (NIMBO_DATA_DIR or ~/.nimbo).
	DataDir string
	// DockerBin is the Docker CLI to run (NIMBO_DOCKER_BIN).
	DockerBin string
}

// DefaultConfig is what a run with an empty environment gets. The Go build is the desktop app, hence "desktop".
func DefaultConfig() Config {
	return Config{
		Port: 8080, Host: "127.0.0.1", EmulatorHost: "localhost", Distribution: "desktop", DockerBin: "docker",
		HostEnv: map[string]string{},
	}
}

// ConfigFromEnv reads the same variables as the Kotlin NimboConfig: NIMBO_PORT, NIMBO_API_TOKEN, NIMBO_HOST,
// NIMBO_EMULATOR_HOST, NIMBO_ALLOWED_HOSTS, NIMBO_DISTRIBUTION (binary, desktop or docker), NIMBO_DATA_DIR and
// NIMBO_DOCKER_BIN. Blank values fall back to the defaults. home is the user's home folder.
func ConfigFromEnv(env map[string]string, home string) Config {
	c := DefaultConfig()
	if env == nil {
		env = map[string]string{}
	}
	c.HostEnv = env
	if p, err := strconv.Atoi(env["NIMBO_PORT"]); err == nil {
		c.Port = p
	}
	if strings.TrimSpace(env["NIMBO_API_TOKEN"]) != "" {
		c.APIToken = env["NIMBO_API_TOKEN"]
	}
	if strings.TrimSpace(env["NIMBO_HOST"]) != "" {
		c.Host = env["NIMBO_HOST"]
	}
	if strings.TrimSpace(env["NIMBO_EMULATOR_HOST"]) != "" {
		c.EmulatorHost = env["NIMBO_EMULATOR_HOST"]
	}
	switch d := env["NIMBO_DISTRIBUTION"]; d {
	case "binary", "desktop", "docker":
		c.Distribution = d
	}
	seen := map[string]bool{}
	for _, h := range strings.Split(env["NIMBO_ALLOWED_HOSTS"], ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !seen[h] {
			seen[h] = true
			c.AllowedHosts = append(c.AllowedHosts, h)
		}
	}
	c.DataDir = DataDir(env, home)
	if b := env["NIMBO_DOCKER_BIN"]; b != "" {
		c.DockerBin = b
	}
	return c
}
