//go:build windows

package diag

import (
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Какие процессы смотрим: само приложение и две программы, которые оно
// запускает. Имя приложения берём своё — у пробной сборки оно другое.
func watchedNames() map[string]string {
	self := "songrequest.exe"
	if exe, err := os.Executable(); err == nil {
		self = strings.ToLower(exe[strings.LastIndexAny(exe, `\/`)+1:])
	}
	return map[string]string{
		self:         "приложение",
		"xray.exe":   "xray",
		"mpv.exe":    "mpv",
		"yt-dlp.exe": "ytdlp",
	}
}

var procMemInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCounters — то, что отдаёт K32GetProcessMemoryInfo.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

func sampleProcs() map[string]procSample {
	names := watchedNames()
	out := map[string]procSample{}

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer windows.CloseHandle(snap)

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		label, ok := names[strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))]
		if !ok {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue
		}
		// xray бывает и чужой: его же возят VPN-программы (Happ, v2rayN). Свой
		// лежит в папке приложения — по пути и отличаем, иначе цифры чужого
		// VPN легли бы на наш обход.
		if label == "xray" && !strings.Contains(strings.ToLower(imagePath(h)), "songrequest") {
			label = "чужой_xray"
		}
		var c, x, k, u windows.Filetime
		s := out[label]
		if windows.GetProcessTimes(h, &c, &x, &k, &u) == nil {
			s.cpu += ftDur(k) + ftDur(u)
		}
		var m processMemoryCounters
		m.cb = uint32(unsafe.Sizeof(m))
		if r, _, _ := procMemInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&m)), uintptr(m.cb)); r != 0 {
			s.memMB += uint64(m.workingSetSize) >> 20
		}
		windows.CloseHandle(h)
		s.count++
		s.at = time.Now()
		out[label] = s
	}
	return out
}

// ftDur — время процессора из записи Windows (сотни наносекунд).
func ftDur(f windows.Filetime) time.Duration {
	return time.Duration(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) * 100
}

// imagePath — полный путь к программе процесса.
func imagePath(h windows.Handle) string {
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// systemVPN — включённые сетевые адаптеры, похожие на VPN на весь компьютер.
//
// 11.10 так нашлась причина жалобы «с приложением тормозит весь интернет»: на
// машине работал VPN в режиме «весь компьютер» (адаптер Happ Tunnel), и через
// него шло всё. Наше приложение так не делает, но поверх такого VPN наш обход
// получается туннелем в туннеле. Пишем имена — по журналу это сразу видно.
func systemVPN() []string {
	var size uint32 = 15000
	var out []string
	for i := 0; i < 3; i++ {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, 0, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil
		}
		for a := first; a != nil; a = a.Next {
			if a.OperStatus != windows.IfOperStatusUp {
				continue
			}
			desc := windows.UTF16PtrToString(a.Description)
			name := windows.UTF16PtrToString(a.FriendlyName)
			low := strings.ToLower(desc + " " + name)
			// Служебные адаптеры Windows для IPv6 — не VPN, хоть в имени и «tunnel».
			if strings.Contains(low, "teredo") || strings.Contains(low, "isatap") || strings.Contains(low, "6to4") {
				continue
			}
			for _, w := range []string{"tun", "tap-", "wintun", "wireguard", "vpn", "amnezia", "happ", "outline", "openvpn"} {
				if strings.Contains(low, w) {
					out = append(out, desc)
					break
				}
			}
		}
		return out
	}
	return out
}
