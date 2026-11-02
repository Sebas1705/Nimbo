package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// runServe is `nimbo serve`: the headless server for Docker, CI and smoke tests. It listens on
// 127.0.0.1:NIMBO_PORT (8080 by default); inside a container NIMBO_HOST=0.0.0.0 makes the published port reach it.
// Flags win over the environment. Returns the process exit code.
func runServe(args []string, environ []string, stdout io.Writer) int {
	env := envMap(environ)
	home, _ := os.UserHomeDir()

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stdout)
	host := fs.String("host", firstNonBlank(env["NIMBO_HOST"], "127.0.0.1"), "address to listen on (NIMBO_HOST)")
	portDefault := 8080
	if p, err := strconv.Atoi(strings.TrimSpace(env["NIMBO_PORT"])); err == nil && p >= 0 && p < 65536 {
		portDefault = p
	}
	port := fs.Int("port", portDefault, "port to listen on (NIMBO_PORT)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var sink io.Writer = stdout
	if env["NIMBO_LOG_FILE"] == "1" {
		if f, err := openLog(shellDataDir(env, home)); err == nil {
			defer f.Close()
			// The exe is a windowed program: its stdout may not exist, which must not stop the file being written.
			sink = io.MultiWriter(f, lenientWriter{stdout})
		}
	}
	logger := log.New(sink, "", log.LstdFlags)

	// An existing Nimbo on the port is reused; a port held by something else makes Nimbo pick another one.
	plan := planLaunch(*port, probeIsNimbo, probeIsFree, probeFreePort)
	if plan.Kind == openExisting {
		logger.Printf("Nimbo already runs on port %d; nothing to start.", plan.Port)
		return 0
	}
	if *port == 0 {
		plan.Port = probeFreePort()
	}

	if !loopbackNames[strings.ToLower(*host)] {
		logger.Printf("Listening on %s: publish the port on loopback only (-p 127.0.0.1:%d:%d); Nimbo can control Docker.", *host, plan.Port, plan.Port)
	}

	api, closer := buildAPI(env, home, firstNonBlank(env["NIMBO_DISTRIBUTION"], "jar"), *host, plan.Port)
	srv := &http.Server{
		Handler:           serveRouter(api, uiFS(), parseAllowedHosts(env)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(plan.Port)))
	if err != nil {
		logger.Printf("Cannot listen on %s:%d: %v", *host, plan.Port, err)
		return 1
	}
	logger.Printf("Nimbo %s listening on http://%s", shownVersion(), net.JoinHostPort(*host, strconv.Itoa(plan.Port)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()

	code := 0
	select {
	case <-ctx.Done():
		logger.Printf("Stopping Nimbo")
	case err := <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("Server stopped: %v", err)
			code = 1
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	if closer != nil {
		_ = closer.Close()
	}
	return code
}

// lenientWriter reports every write as successful.
type lenientWriter struct{ w io.Writer }

func (l lenientWriter) Write(p []byte) (int, error) {
	_, _ = l.w.Write(p)
	return len(p), nil
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
