//go:build windows

package desktop

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// Opt-in: creates real windows, but never connects to Spotify or uses credentials.
func TestNativeAuthAutoCloseLeavesMainWindowResponsive(t *testing.T) {
	if os.Getenv("SONGREQUEST_NATIVE_AUTH_TEST") != "1" {
		t.Skip("requires interactive Windows desktop")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	loginLoaded := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			select {
			case loginLoaded <- struct{}{}:
			default:
			}
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Local auth test</title><p>Test completed</p>"))
	}))
	defer srv.Close()
	root := t.TempDir()
	main := webview.NewWithOptions(webview.WebViewOptions{DataPath: filepath.Join(root, "main"), WindowOptions: webview.WindowOptions{Title: "Songrequest native regression test"}})
	if main == nil {
		t.Fatal("main webview unavailable")
	}
	defer procDestroyWindow.Call(uintptr(main.Window()))
	responsive := false
	if err := main.Bind("stillAlive", func() {
		responsive = true
		main.Dispatch(main.Terminate)
	}); err != nil {
		t.Fatal(err)
	}
	main.Navigate(srv.URL)
	errCh := make(chan error, 1)
	go func() {
		err := OpenAuth(AuthOptions{URL: srv.URL + "/callback", DataPath: filepath.Join(root, "auth"), Title: "Songrequest auth regression test"})
		if err == nil {
			go func() {
				select {
				case <-loginLoaded:
				case <-time.After(10 * time.Second):
					return
				}
				title, _ := windows.UTF16PtrFromString("Songrequest auth manual regression test")
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					hwnd, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
					if hwnd != 0 {
						user32.NewProc("PostMessageW").Call(hwnd, wmClose, 0, 0)
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
			}()
			err = OpenAuth(AuthOptions{URL: srv.URL + "/login", DataPath: filepath.Join(root, "auth"), Title: "Songrequest auth manual regression test"})
		}
		errCh <- err
		main.Dispatch(func() { main.Eval("window.stillAlive()") })
	}()
	timedOut := false
	timer := time.AfterFunc(25*time.Second, func() {
		main.Dispatch(func() { timedOut = true; main.Terminate() })
	})
	defer timer.Stop()
	main.Run()
	if timedOut || !responsive {
		t.Fatal("auth did not close or main window stopped responding")
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}
