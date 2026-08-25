package cmd

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInternalProxyHelper(t *testing.T) {
	if os.Getenv("AIX_TEST_INTERNAL_PROXY") != "1" {
		return
	}
	if err := runInternalProxyService(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestInternalProxyStartsWithoutPublicCommand(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	home := t.TempDir()
	aixDir := filepath.Join(home, ".aix")
	if err := os.MkdirAll(aixDir, 0700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("listen = %q\ngateway_key = %q\n", addr, "test-gateway-key")
	if err := os.WriteFile(filepath.Join(aixDir, "proxy.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestInternalProxyHelper$")
	child.Env = append(os.Environ(), "HOME="+home, "AIX_TEST_INTERNAL_PROXY=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Signal(os.Interrupt)
		_, _ = child.Process.Wait()
	}()

	url := "http://" + addr + "/health"
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("internal Claude gateway did not answer at %s", url)
}
