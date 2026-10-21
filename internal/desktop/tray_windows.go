//go:build windows

package desktop

import (
	"runtime"
	"time"

	"fyne.io/systray"

	"songrequest/internal/i18n"
)

// RunTray заводит значок возле часов и крутится до StopTray.
//
// Вызывать в отдельной горутине: работа блокирующая. Внутри поток закрепляется
// за горутиной — у значка, как и у окна, свой цикл сообщений Windows, и
// приходят они только в тот поток, где значок создан.
func RunTray(o TrayOptions) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	systray.Run(func() {
		if o.IconPath != "" {
			systray.SetIconFromFilePath(o.IconPath)
		}
		tooltip := func() {
			if o.TooltipFn != nil {
				systray.SetTooltip(o.TooltipFn())
			} else if o.Tooltip != "" {
				systray.SetTooltip(o.Tooltip)
			}
		}
		tooltip()

		// Левый клик открывает панель. Меню остаётся на правой кнопке — так
		// же, как решил владелец про выход: чтобы случайное нажатие ничего
		// не закрывало.
		if o.OnOpen != nil {
			systray.SetOnTapped(o.OnOpen)
		}

		открыть := systray.AddMenuItem(i18n.T("Открыть панель"), i18n.T("Показать окно программы"))
		копия := systray.AddMenuItem(i18n.T("Скопировать адрес виджета для OBS"), i18n.F("Скопировать адрес: %s", o.WidgetURL))
		systray.AddSeparator()
		выход := systray.AddMenuItem(i18n.T("Выход"), i18n.T("Закрыть программу совсем"))

		// Язык меняется в настройках на ходу — подписи меню догоняют его.
		// Меню строится один раз, поэтому просто переименовываем пункты.
		go func() {
			shown := i18n.Lang()
			for range time.Tick(2 * time.Second) {
				if l := i18n.Lang(); l != shown {
					shown = l
					открыть.SetTitle(i18n.T("Открыть панель"))
					открыть.SetTooltip(i18n.T("Показать окно программы"))
					копия.SetTitle(i18n.T("Скопировать адрес виджета для OBS"))
					копия.SetTooltip(i18n.F("Скопировать адрес: %s", o.WidgetURL))
					выход.SetTitle(i18n.T("Выход"))
					выход.SetTooltip(i18n.T("Закрыть программу совсем"))
					tooltip()
				}
			}
		}()

		go func() {
			for {
				select {
				case <-открыть.ClickedCh:
					if o.OnOpen != nil {
						o.OnOpen()
					}
				case <-копия.ClickedCh:
					err := setClipboard(o.WidgetURL)
					if o.OnCopy != nil {
						o.OnCopy(err)
					}
				case <-выход.ClickedCh:
					if o.OnQuit != nil {
						o.OnQuit()
					}
				}
			}
		}()
	}, nil)
}

// StopTray убирает значок возле часов. Без этого он остаётся висеть до тех
// пор, пока по нему не проведёшь мышью, — Windows чистит такие «мёртвые»
// значки только при наведении, и человек думает, что программа не закрылась.
func StopTray() { systray.Quit() }
