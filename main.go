// Command caire-studio serves a local web GUI for experimenting with
// esimov/caire content-aware (seam carving) image resizing.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"caire-studio/internal/server"
)

//go:embed all:web
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:7777", "address to listen on")
	open := flag.Bool("open", true, "open the UI in the default browser")
	flag.Parse()

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	url := browserURL(ln.Addr())
	log.Printf("Caire Studio running at %s", url)

	if *open {
		go func() {
			if err := openBrowser(url); err != nil {
				log.Printf("could not open browser: %v", err)
			}
		}()
	}

	srv := &http.Server{
		Handler:           server.New(static),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: progress is streamed over long-lived SSE connections.
	}
	log.Fatal(srv.Serve(ln))
}

func browserURL(a net.Addr) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return "http://" + a.String() + "/"
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s/", net.JoinHostPort(host, port))
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
