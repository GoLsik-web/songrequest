//go:build windows

package desktop

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Заставка запуска «Пластинка» (просьба владельца 25.10).
//
// На экране нет никакого окна — только сам знак: прозрачное окно Windows с
// настоящей полупрозрачностью (UpdateLayeredWindow) проигрывает заранее
// нарисованные кадры (tools/splash: preview.html → export.html → pack.py).
// Когда кадры кончились, главное окно появляется ровно на месте пластинки —
// круглым — и раскрывается в прямоугольник программы. Пластинка буквально
// становится окном, а не прячется под ним.
//
// Почему не WebView2: прозрачный фон он в обычном окне не умеет, вместо
// рабочего стола получается белый прямоугольник (проверено 25.10).

//go:embed splash/frames.bin
var splashFrames []byte

//go:embed splash/sound.wav
var splashSound []byte

const (
	growFor     = 750 * time.Millisecond
	fadeFor     = 220 * time.Millisecond
	waitPageFor = 6 * time.Second // дольше страницу не ждём — раскрываемся как есть
)

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")
	winmm = windows.NewLazySystemDLL("winmm.dll")

	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procPeekMessage         = user32.NewProc("PeekMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procSetWindowRgn        = user32.NewProc("SetWindowRgn")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")

	procPlaySound = winmm.NewProc("PlaySoundW")
)

// Наименьший размер, до которого можно сжать окно. Пока пластинка
// раскрывается в окно, он почти нулевой: иначе Windows сразу растянула бы
// круг до рабочего минимума.
var minTrackW, minTrackH atomic.Int32

func init() {
	minTrackW.Store(minWidth)
	minTrackH.Store(minHeight)
}

// ── кадры ───────────────────────────────────────────────────────────

type splashPack struct {
	fps, w, h           int
	discX, discY, discR int // пластинка в последнем кадре, точки кадра
	frames              [][]byte
}

func readPack(b []byte) (*splashPack, error) {
	if len(b) < 22 || string(b[:4]) != "SRSP" {
		return nil, errors.New("кадры заставки повреждены")
	}
	u16 := func(o int) int { return int(binary.LittleEndian.Uint16(b[o:])) }
	p := &splashPack{fps: u16(4), w: u16(6), h: u16(8), discX: u16(14), discY: u16(16), discR: u16(18)}
	n, off := u16(20), 22
	if p.w == 0 || p.h == 0 || p.w > 4096 || p.h > 4096 || p.fps == 0 || p.fps > 120 || n == 0 || n > 1200 {
		return nil, errors.New("неверные размеры или частота кадров заставки")
	}
	for i := 0; i < n; i++ {
		if off+4 > len(b) {
			return nil, errors.New("кадры заставки обрезаны")
		}
		l := int(binary.LittleEndian.Uint32(b[off:]))
		off += 4
		if l == 0 || l > len(b)-off {
			return nil, errors.New("кадры заставки обрезаны")
		}
		p.frames = append(p.frames, b[off:off+l])
		off += l
	}
	if p.fps == 0 || len(p.frames) == 0 {
		return nil, errors.New("в заставке нет кадров")
	}
	return p, nil
}

// bgra раскладывает кадр в память окна: BGRA с предумноженной альфой, как
// хочет UpdateLayeredWindow, с масштабом под экран (сглаживанием).
func bgra(src *image.RGBA, dst []byte, dw, dh int) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if dw == sw && dh == sh {
		for i := 0; i+3 < len(src.Pix) && i+3 < len(dst); i += 4 {
			dst[i], dst[i+1], dst[i+2], dst[i+3] = src.Pix[i+2], src.Pix[i+1], src.Pix[i], src.Pix[i+3]
		}
		return
	}
	fx, fy := float64(sw)/float64(dw), float64(sh)/float64(dh)
	for y := 0; y < dh; y++ {
		sy := (float64(y)+0.5)*fy - 0.5
		y0 := int(math.Floor(sy))
		ty := sy - float64(y0)
		y1 := y0 + 1
		if y0 < 0 {
			y0 = 0
		}
		if y1 >= sh {
			y1 = sh - 1
		}
		r0, r1 := src.Pix[y0*src.Stride:], src.Pix[y1*src.Stride:]
		row := dst[y*dw*4:]
		for x := 0; x < dw; x++ {
			sx := (float64(x)+0.5)*fx - 0.5
			x0 := int(math.Floor(sx))
			tx := sx - float64(x0)
			x1 := x0 + 1
			if x0 < 0 {
				x0 = 0
			}
			if x1 >= sw {
				x1 = sw - 1
			}
			a, b, c, d := x0*4, x1*4, x0*4, x1*4
			for ch := 0; ch < 4; ch++ {
				top := float64(r0[a+ch])*(1-tx) + float64(r0[b+ch])*tx
				bot := float64(r1[c+ch])*(1-tx) + float64(r1[d+ch])*tx
				v := uint8(top*(1-ty) + bot*ty + 0.5)
				// RGBA → BGRA
				switch ch {
				case 0:
					row[x*4+2] = v
				case 1:
					row[x*4+1] = v
				case 2:
					row[x*4] = v
				case 3:
					row[x*4+3] = v
				}
			}
		}
	}
}

func decode(b []byte) (*image.RGBA, error) {
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	// image.RGBA — предумноженная альфа, ровно то, что нужно окну.
	out := image.NewRGBA(im.Bounds())
	draw.Draw(out, out.Rect, im, im.Bounds().Min, draw.Src)
	return out, nil
}

// ── окно-заставка ───────────────────────────────────────────────────

type splash struct {
	pack       *splashPack
	scale      float64
	x, y, w, h int32 // где окно на экране, точки экрана

	ended chan struct{} // последний кадр показан
	fade  chan struct{} // пора гаснуть
	once  sync.Once
}

// discRect — пластинка последнего кадра в точках экрана.
func (s *splash) discRect() rect {
	cx := float64(s.x) + float64(s.pack.discX)*s.scale
	cy := float64(s.y) + float64(s.pack.discY)*s.scale
	r := float64(s.pack.discR) * s.scale
	return rect{int32(cx - r), int32(cy - r), int32(cx + r), int32(cy + r)}
}

func (s *splash) fadeOut() { s.once.Do(func() { close(s.fade) }) }

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type size struct{ CX, CY int32 }
type blend struct{ Op, Flags, Alpha, Format byte }

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

var splashClass sync.Once

// startSplash открывает заставку и сразу возвращается: кадры крутятся в
// своём потоке, пока главное окно поднимает движок страницы.
func startSplash(sound bool, center point) *splash {
	pack, err := readPack(splashFrames)
	if err != nil {
		return nil
	}
	s := &splash{pack: pack, ended: make(chan struct{}), fade: make(chan struct{})}
	started := make(chan bool)
	go s.run(sound, center, started)
	if !<-started {
		return nil
	}
	return s
}

func (s *splash) run(sound bool, center point, started chan<- bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	name, _ := windows.UTF16PtrFromString("songrequest-splash")
	splashClass.Do(func() {
		wc := wndClassEx{
			Size:      uint32(unsafe.Sizeof(wndClassEx{})),
			WndProc:   windows.NewCallback(func(h, m, w, l uintptr) uintptr { r, _, _ := procDefWindowProc.Call(h, m, w, l); return r }),
			ClassName: name,
		}
		procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	})

	const exLayered, exTopmost, exTool, exNoActivate, exTransparent = 0x80000, 0x8, 0x80, 0x08000000, 0x20
	hwnd, _, _ := procCreateWindowEx.Call(exLayered|exTopmost|exTool|exNoActivate|exTransparent,
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0x80000000, // WS_POPUP
		0, 0, 1, 1, 0, 0, 0, 0)
	if hwnd == 0 {
		started <- false
		return
	}
	defer procDestroyWindow.Call(hwnd)

	s.scale = float64(dpiOf(hwnd)) / 96
	s.w, s.h = int32(float64(s.pack.w)*s.scale), int32(float64(s.pack.h)*s.scale)
	// Пластинку ставим ровно в центр будущего окна: из неё оно и вырастет.
	s.x = center.X - int32(float64(s.pack.discX)*s.scale)
	s.y = center.Y - int32(float64(s.pack.discY)*s.scale)

	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	defer procDeleteDC.Call(mem)
	bi := bitmapInfoHeader{Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: s.w, Height: -s.h, Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	dib, _, _ := procCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 {
		started <- false
		return
	}
	defer procDeleteObject.Call(dib)
	procSelectObject.Call(mem, dib)
	buf := unsafe.Slice((*byte)(bits), int(s.w)*int(s.h)*4)

	show := func(alpha byte) {
		pos, sz, src := point{s.x, s.y}, size{s.w, s.h}, point{}
		bl := blend{0, 0, alpha, 1} // AC_SRC_OVER, AC_SRC_ALPHA
		procUpdateLayeredWindow.Call(hwnd, screen, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&sz)),
			mem, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&bl)), 2) // ULW_ALPHA
	}
	pump := func() {
		var m msgT
		for {
			if r, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1); r == 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}

	// Кадры распаковываются заранее, в двух потоках: PNG и масштаб на
	// каждый кадр не должны отнимать время у показа.
	n := len(s.pack.frames)
	frames := prefetchFrames(s.pack.frames, decode)

	started <- true
	procShowWindow.Call(hwnd, 4) // SW_SHOWNOACTIVATE
	if sound && len(splashSound) > 0 {
		procPlaySound.Call(uintptr(unsafe.Pointer(&splashSound[0])), 0, 0x1|0x2|0x4) // ASYNC|NODEFAULT|MEMORY
	}

	start := time.Now()
	step := time.Second / time.Duration(s.pack.fps)
	for i := 0; i < n; i++ {
		im := <-frames
		// Отстали (медленный компьютер) — пропускаем кадр, а не тянем время.
		if late := time.Since(start) - time.Duration(i+1)*step; late > step && i < n-1 {
			continue
		}
		if im != nil {
			bgra(im, buf, int(s.w), int(s.h))
			show(255)
		}
		pump()
		if wait := time.Until(start.Add(time.Duration(i+1) * step)); wait > 0 {
			time.Sleep(wait)
		}
	}
	close(s.ended)

	// Последний кадр держим, пока главное окно не встанет на место пластинки.
	for {
		select {
		case <-s.fade:
			t0 := time.Now()
			for {
				p := float64(time.Since(t0)) / float64(fadeFor)
				if p >= 1 {
					return
				}
				show(byte(255 * (1 - p)))
				pump()
				time.Sleep(10 * time.Millisecond)
			}
		case <-time.After(15 * time.Millisecond):
			pump()
		}
	}
}

// ── раскрытие главного окна ─────────────────────────────────────────

func dpiOf(hwnd uintptr) int32 {
	if procGetDpiForWindow.Find() == nil {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
			return int32(dpi)
		}
	}
	return 96
}

func workArea(hwnd uintptr) (rect, bool) {
	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest)
	if mon == 0 {
		return rect{}, false
	}
	mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return rect{}, false
	}
	return mi.RcWork, true
}

func (w *Window) setRect(r rect) {
	procSetWindowPos.Call(w.hwnd, 0, uintptr(r.Left), uintptr(r.Top),
		uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpNoZOrder|swpNoActivate)
}

// setRound обрезает окно скруглённым прямоугольником (круг — частный случай).
func (w *Window) setRound(width, height, radius int32) {
	rgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(width+1), uintptr(height+1), uintptr(radius*2), uintptr(radius*2))
	procSetWindowRgn.Call(w.hwnd, rgn, 1) // окно забирает регион себе, удалять не надо
}

// targetRect — где встанет окно программы, точки экрана. Нужен ещё до
// окна: по нему ставится заставка.
func targetRect(g Geometry) rect {
	if g.X != 0 || g.Y != 0 {
		return rect{int32(g.X), int32(g.Y), int32(g.X + g.Width), int32(g.Y + g.Height)}
	}
	work := rect{0, 0, 1920, 1040}
	if mon, _, _ := procMonitorFromPoint.Call(0, monitorNearest); mon != 0 {
		mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok != 0 {
			work = mi.RcWork
		}
	}
	if g.Maximized {
		return work
	}
	x := work.Left + (work.Right-work.Left-int32(g.Width))/2
	y := work.Top + (work.Bottom-work.Top-int32(g.Height))/2
	return rect{x, y, x + int32(g.Width), y + int32(g.Height)}
}

// reveal ждёт конца кадров и готовности страницы, показывает главное окно
// кругом ровно на месте пластинки и раскрывает его форму до прямоугольника.
//
// Само окно всё это время стоит на своём месте и в своём размере — растёт
// только видимая его часть (регион). Менять размер окна на каждом шаге
// нельзя: страница не успевает перерисоваться, и в кадре полосы мусора.
func (w *Window) reveal(s *splash, target Geometry, to rect) {
	defer s.fadeOut()
	select {
	case <-s.ended:
	case <-w.closed:
		return
	}
	select {
	case <-w.pageReady:
	case <-time.After(waitPageFor):
	case <-w.closed:
		return
	}

	disc := s.discRect()
	from := rect{disc.Left - to.Left, disc.Top - to.Top, disc.Right - to.Left, disc.Bottom - to.Top}
	full := rect{0, 0, to.Right - to.Left, to.Bottom - to.Top}
	dpi := float64(dpiOf(w.hwnd)) / 96
	region := func(r rect, radius int32) {
		rgn, _, _ := procCreateRoundRectRgn.Call(uintptr(r.Left), uintptr(r.Top), uintptr(r.Right+1), uintptr(r.Bottom+1),
			uintptr(radius*2), uintptr(radius*2))
		procSetWindowRgn.Call(w.hwnd, rgn, 1) // окно забирает регион себе, удалять не надо
	}

	done := make(chan struct{})
	w.wv.Dispatch(func() {
		defer close(done)
		if w.quitting.Load() {
			return
		}
		region(from, (from.Right-from.Left)/2)
		procShowWindow.Call(w.hwnd, swShow)
		procSetForegroundWin.Call(w.hwnd)
		w.wv.Eval("window.srReveal && window.srReveal(" + itoa(int(growFor/time.Millisecond)) + ")")
	})
	select {
	case <-done:
	case <-w.closed:
		return
	}
	// Пластинка заставки гаснет под окном, которое уже стоит на её месте.
	time.AfterFunc(60*time.Millisecond, s.fadeOut)

	lerp := func(a, b int32, t float64) int32 { return a + int32(math.Round(float64(b-a)*t)) }
	start := time.Now()
	for {
		if w.quitting.Load() {
			return
		}
		p := float64(time.Since(start)) / float64(growFor)
		if p > 1 {
			p = 1
		}
		// Быстрый рывок и долгое мягкое «доезжание».
		t := 1 - math.Pow(2, -10*p)
		if p == 1 {
			t = 1
		}
		r := rect{lerp(from.Left, full.Left, t), lerp(from.Top, full.Top, t), lerp(from.Right, full.Right, t), lerp(from.Bottom, full.Bottom, t)}
		short := r.Right - r.Left
		if h := r.Bottom - r.Top; h < short {
			short = h
		}
		radius := lerp(short/2, int32(10*dpi), math.Min(1, t*1.15))
		w.wv.Dispatch(func() {
			if !w.quitting.Load() {
				region(r, radius)
			}
		})
		if p == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.wv.Dispatch(func() {
		if w.quitting.Load() {
			return
		}
		// Регион снимаем: углы дальше скругляет сама Windows, с тенью.
		procSetWindowRgn.Call(w.hwnd, 0, 1)
		minTrackW.Store(minWidth)
		minTrackH.Store(minHeight)
		w.splashing.Store(false)
		if target.Maximized {
			procShowWindow.Call(w.hwnd, swShowMaximized)
		}
	})
}

// prefetchFrames preserves frame order while bounding decoded images in memory.
func prefetchFrames(frames [][]byte, decode func([]byte) (*image.RGBA, error)) <-chan *image.RGBA {
	out := make(chan *image.RGBA)
	ready := make([]chan *image.RGBA, len(frames))
	for i := range ready {
		ready[i] = make(chan *image.RGBA, 1)
	}
	slots := make(chan struct{}, 4)
	for i := 0; i < cap(slots); i++ {
		slots <- struct{}{}
	}
	jobs := make(chan int)
	go func() {
		defer close(jobs)
		for i := range frames {
			<-slots
			jobs <- i
		}
	}()
	for worker := 0; worker < 2; worker++ {
		go func() {
			for i := range jobs {
				im, _ := decode(frames[i])
				ready[i] <- im
			}
		}()
	}
	go func() {
		defer close(out)
		for _, ch := range ready {
			out <- <-ch
			slots <- struct{}{}
		}
	}()
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
