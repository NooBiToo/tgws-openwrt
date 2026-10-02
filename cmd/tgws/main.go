// Команда tgws — прозрачный прокси Telegram: принимает TCP-соединения,
// перенаправленные nftables, и ведёт их через WebSocket.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tgws/internal/dcmap"
	"tgws/internal/origdst"
	"tgws/internal/proxy"
	"tgws/internal/sockmark"
	"tgws/internal/ws"
)

// version подставляется при сборке: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	listen := flag.String("listen", ":5454", "адрес, на котором принимать перенаправленные соединения")
	dcIP := flag.String("dc-ip", "2:149.154.167.220,4:149.154.167.220", "DC:IP, на которые открывать WebSocket (пусто — только прямой TCP)")
	mark := flag.Int("mark", 0x7467, "SO_MARK исходящих сокетов (0 — без метки)")
	statsPath := flag.String("stats", "", "файл со счётчиками в JSON (пусто — не писать)")
	verbose := flag.Bool("v", false, "подробный журнал по каждому соединению")
	showVersion := flag.Bool("version", false, "напечатать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	targets, err := dcmap.ParseTargets(*dcIP)
	if err != nil {
		log.Fatalf("tgws: %v", err)
	}

	// Метки времени не нужны: procd пишет вывод в журнал сам.
	log.SetFlags(0)
	logf := func(format string, args ...any) { log.Printf(format, args...) }
	debugf := func(string, ...any) {}
	if *verbose {
		debugf = logf
	}

	cfg := proxy.Config{
		Targets: targets,
		OrigDst: origdst.Get,
		DialWS: func(ctx context.Context, target, domain, path string) (proxy.WSConn, error) {
			d := ws.Dialer{
				Addr:       net.JoinHostPort(target, "443"),
				ServerName: domain,
				Path:       path,
				Mark:       *mark,
				Timeout:    5 * time.Second,
			}
			c, err := d.Dial(ctx)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		DialTCP: func(ctx context.Context, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second, Control: sockmark.Control(*mark)}
			return d.DialContext(ctx, "tcp", addr)
		},
		Logf:   logf,
		Debugf: debugf,
	}
	srv := proxy.New(cfg)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("tgws: %v", err)
	}
	logf("tgws %s listening on %s, WebSocket for DC targets %v", version, ln.Addr(), targets)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *statsPath != "" {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				if err := srv.Stats().WriteFile(*statsPath); err != nil {
					debugf("stats: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}

	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintln(os.Stderr, "tgws:", err)
		os.Exit(1)
	}
}
