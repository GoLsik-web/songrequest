//go:build windows

// Package procjob привязывает чужие программы (xray, mpv) к жизни приложения.
//
// 12.10 нашлось: приложение закрыли не через «Выйти», а убили (диспетчер
// задач, обновление, падение) — и xray.exe остался висеть. Следующий запуск
// поднимал свой, а старый так и жил до перезагрузки. Windows умеет это сама:
// процессы в «задании» с флагом KILL_ON_JOB_CLOSE умирают, как только
// закрывается последний его описатель, то есть вместе с нами.
package procjob

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	once sync.Once
	job  windows.Handle
)

func handle() windows.Handle {
	once.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			return
		}
		job = h
	})
	return job
}

// Bind привязывает уже запущенный процесс. Не вышло — не беда: процесс
// просто проживёт по-старому, до явной остановки.
func Bind(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	j := handle()
	if j == 0 {
		return
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(p)
	_ = windows.AssignProcessToJobObject(j, p)
}
