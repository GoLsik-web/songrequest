//go:build windows

package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
	"time"
)

func TestEmbeddedSplashFramesAreUsable(t *testing.T) {
	p, err := readPack(splashFrames)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, len(p.frames) - 1} {
		im, err := png.Decode(bytes.NewReader(p.frames[i]))
		if err != nil {
			t.Fatal(err)
		}
		if im.Bounds().Dx() != p.w || im.Bounds().Dy() != p.h {
			t.Fatal("splash header does not match images")
		}
	}
	for _, data := range [][]byte{nil, splashFrames[:10], splashFrames[:len(splashFrames)-1]} {
		if _, err := readPack(data); err == nil {
			t.Fatal("damaged splash accepted")
		}
	}
	broken := append([]byte(nil), splashFrames...)
	binary.LittleEndian.PutUint16(broken[6:], 0)
	if _, err := readPack(broken); err == nil {
		t.Fatal("zero-width splash accepted")
	}
}

func TestSplashPrefetchKeepsOrderWithBoundedQueue(t *testing.T) {
	frames := make([][]byte, 40)
	for i := range frames {
		frames[i] = []byte{byte(i)}
	}
	output := prefetchFrames(frames, func(b []byte) (*image.RGBA, error) {
		if b[0]%2 == 1 {
			time.Sleep(time.Millisecond)
		}
		return image.NewRGBA(image.Rect(0, 0, int(b[0])+1, 1)), nil
	})
	for i := range frames {
		select {
		case im := <-output:
			if im == nil || im.Bounds().Dx() != i+1 {
				t.Fatalf("frame %d out of order", i)
			}
		case <-time.After(time.Second):
			t.Fatal("frame prefetch deadlocked")
		}
	}
	if _, ok := <-output; ok {
		t.Fatal("frame stream did not close")
	}
}
func TestQuitDuringSplashCancelsReveal(t *testing.T) {
	w := &Window{closed: make(chan struct{}), pageReady: make(chan struct{})}
	close(w.closed)
	sp := &splash{ended: make(chan struct{}), fade: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); w.reveal(sp, Geometry{}, rect{}) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reveal survived window close")
	}
	select {
	case <-sp.fade:
	default:
		t.Fatal("splash was left on screen")
	}
}
