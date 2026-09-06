//go:build windows

package spotifyapp

import (
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Разговор с Windows: перебрать окна, найти окно программы Spotify и взять его
// заголовок.
//
// Окно ищем не по названию класса и не по заголовку, а по программе, которой
// оно принадлежит: заголовок как раз меняется каждые три минуты, а класс у
// Spotify такой же, как у любой программы на движке Chrome, — по нему легко
// поймать чужое окно.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procEnumWindows     = user32.NewProc("EnumWindows")
	procGetWindowTextW  = user32.NewProc("GetWindowTextW")
	procGetWindowTextLn = user32.NewProc("GetWindowTextLengthW")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procGetWindowPID    = user32.NewProc("GetWindowThreadProcessId")

	procIsWindow = user32.NewProc("IsWindow")

	procOpenProcess     = kernel32.NewProc("OpenProcess")
	procQueryImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle     = kernel32.NewProc("CloseHandle")
)

// processQueryLimited — самое скромное право на процесс: узнать, что это за
// программа. Полный доступ нам не нужен и мог бы не даваться вовсе.
const processQueryLimited = 0x1000

// enumOnce хранит обратный вызов для EnumWindows. Создавать его на каждый
// опрос нельзя: windows.NewCallback выделяет место в таблице, которое не
// освобождается, а опрос идёт раз в секунду весь стрим.
var (
	enumOnce sync.Once
	enumProc uintptr

	// found защищает результат перебора: EnumWindows зовёт обратный вызов в
	// том же потоке, но опрашивать нас могут из разных горутин.
	found   struct{ title string }
	foundMu sync.Mutex

	// known — окно Spotify, найденное в прошлый раз.
	//
	// Перебирать все окна системы каждую секунду весь стрим — расточительно:
	// на каждое окно с заголовком приходится открывать процесс и спрашивать
	// его имя. Пока окно живо, спрашиваем только его заголовок.
	known uintptr
)

// Read возвращает то, что играет в программе Spotify на этом компьютере.
//
// Второе значение — удалось ли вообще прочитать. Оно означает «программа
// Spotify запущена и играет»: если она закрыта, свёрнута в трей без окна или
// музыка стоит, читать нечего, и приложение спрашивает Spotify по сети, как
// раньше.
func Read() (Track, Status) {
	title, ok := spotifyWindowTitle()
	if !ok {
		return Track{}, Unknown
	}
	return parseTitle(title)
}

// spotifyWindowTitle находит окно программы Spotify и отдаёт его заголовок.
func spotifyWindowTitle() (string, bool) {
	foundMu.Lock()
	defer foundMu.Unlock()

	// Сначала — окно, найденное в прошлый раз. Проверяем и что оно живо, и
	// что оно по-прежнему принадлежит Spotify: номера закрытых окон Windows
	// выдаёт заново, и на месте закрытого Spotify может оказаться чужое.
	if known != 0 {
		if alive, _, _ := procIsWindow.Call(known); alive != 0 && isSpotify(known) {
			if title := windowText(known); title != "" {
				return title, true
			}
		}
		known = 0
	}

	found.title = ""
	enumOnce.Do(func() {
		enumProc = windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
			if !windowInteresting(hwnd) {
				return 1 // продолжаем перебор
			}
			if !isSpotify(hwnd) {
				return 1
			}
			found.title = windowText(hwnd)
			known = hwnd
			return 0 // нашли, дальше не смотрим
		})
	})
	procEnumWindows.Call(enumProc, 0)

	return found.title, found.title != ""
}

// windowInteresting отсеивает невидимые окна и окна без заголовка: у Spotify
// их несколько, а имя трека пишется только в главном.
func windowInteresting(hwnd uintptr) bool {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return false
	}
	length, _, _ := procGetWindowTextLn.Call(hwnd)
	return length > 0
}

// windowText читает заголовок окна.
func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// isSpotify отвечает, принадлежит ли окно программе Spotify.
func isSpotify(hwnd uintptr) bool {
	var pid uint32
	procGetWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}

	handle, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if handle == 0 {
		return false
	}
	defer procCloseHandle.Call(handle)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	ok, _, _ := procQueryImageNameW.Call(handle, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return false
	}
	name := filepath.Base(windows.UTF16ToString(buf[:size]))
	return strings.EqualFold(name, "Spotify.exe")
}
