package server

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// webFS is the interface bundled into release builds; development builds
// serve it from disk (MAGISEND_WEB, default web/dist).
var webFS fs.FS

func webInterface() fs.FS {
	if webFS != nil {
		return webFS
	}
	return os.DirFS(env("MAGISEND_WEB", "web/dist"))
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// healthcheck asks the local server for /api/health; used by container and
// updater health checks without needing curl.
func healthcheck() int {
	address := env("MAGISEND_LISTEN", "0.0.0.0:8080")
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: 5 * time.Second}
	response, e := client.Get("http://" + net.JoinHostPort(host, port) + "/api/health")
	if e != nil {
		return 1
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return 1
	}
	return 0
}

// Main runs the server. The command passes its build version, the bundled web
// interface (nil serves it from disk) and the API error catalog it embeds.
func Main(buildVersion string, ui fs.FS, errorCatalog []byte) {
	version = buildVersion
	webFS = ui
	if e := SetErrorCatalog(errorCatalog); e != nil {
		log.Fatal(e)
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "version":
			fmt.Println(version)
			return
		case "--healthcheck":
			os.Exit(healthcheck())
		case "--verify-release":
			if len(os.Args) != 4 {
				fmt.Fprintln(os.Stderr, "usage: magisend --verify-release SHA256SUMS SHA256SUMS.sig")
				os.Exit(2)
			}
			if e := verifyRelease(os.Args[2], os.Args[3]); e != nil {
				fmt.Fprintln(os.Stderr, e)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: magisend [--version | --healthcheck | --verify-release SHA256SUMS SHA256SUMS.sig]")
			os.Exit(2)
		}
	}
	a, e := NewApp(env("MAGISEND_DATA", "/var/lib/magisend"), env("MAGISEND_PUBLIC_ORIGIN", ""))
	if e != nil {
		log.Fatal("cannot initialize application: ", e)
	}
	if e = a.SetTrustedProxies(env("MAGISEND_TRUSTED_PROXIES", "")); e != nil {
		log.Fatal(e)
	}
	defer a.db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go a.updates.Run(ctx)
	go a.mailWorker()
	go a.pushWorker()
	go a.notificationWorker()
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			if e := a.Cleanup(time.Now()); e != nil {
				log.Print("cleanup failed; reservations retained")
			}
			a.dueBackup(time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	server := &http.Server{Addr: env("MAGISEND_LISTEN", "0.0.0.0:8080"), Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	// On a stop signal, requests in progress get up to 15 seconds to finish
	// before the database closes; ListenAndServe returns as soon as the
	// shutdown begins, so Main waits for it to end. A running backup stops at
	// its next step and is waited for too.
	drained := make(chan struct{})
	go func() {
		<-ctx.Done()
		a.stopping.Store(true)
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		server.Shutdown(c)
		a.waitBackup(15 * time.Second)
		close(drained)
	}()
	log.Printf("MagiSend %s listening on %s", version, server.Addr)
	if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
	<-drained
}
