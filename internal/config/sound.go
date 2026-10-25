package config

import "strings"

// OrderSound — короткий звук, когда заказ встал в очередь.
//
// Играет виджет в OBS, а не само приложение: так его слышат и стример, и
// зрители, а громкость дополнительно крутится в микшере OBS, как у любого
// источника. Просьба владельца 25.10: «чел заказал — короткий звук на выбор».
type OrderSound struct {
	Enabled bool `json:"enabled"`

	// Sound — встроенный звук (имя из OrderSounds) или "file" — свой файл
	// из File. Встроенные виджет синтезирует сам, файлов для них нет.
	Sound string `json:"sound"`
	// File — загруженный звук, имя как у картинок виджета: 0123…cdef.mp3.
	File string `json:"file"`

	// Volume — громкость в процентах, 1…100.
	Volume int `json:"volume"`

	// Cooldown — не чаще раза в столько секунд. Плейлист или пачка заказов
	// подряд иначе превращаются в пулемёт. 0 — на каждый заказ.
	Cooldown int `json:"cooldown"`
}

// OrderSounds — встроенные звуки. Список закрытый: виджет знает только их.
var OrderSounds = map[string]bool{
	"ding": true, "pop": true, "chime": true, "coin": true,
	"bubble": true, "levelup": true, "vinyl": true, "soft": true,
}

// AudioExt — какие свои звуки принимаем.
var AudioExt = map[string]bool{"mp3": true, "wav": true, "ogg": true}

// SoundName — имя загруженного звука: 16 шестнадцатеричных знаков и
// расширение звука.
func SoundName(name string) bool {
	dot := strings.IndexByte(name, '.')
	if dot != 16 || !AudioExt[name[dot+1:]] {
		return false
	}
	for _, c := range name[:dot] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func DefaultOrderSound() OrderSound {
	return OrderSound{Sound: "ding", Volume: 60, Cooldown: 3}
}

// Normalize чинит руками поправленное и выкидывает непонятное.
func (o *OrderSound) Normalize() {
	if !SoundName(o.File) {
		o.File = ""
	}
	if o.Sound == "file" && o.File == "" || o.Sound != "file" && !OrderSounds[o.Sound] {
		o.Sound = "ding"
	}
	if o.Volume == 0 {
		o.Volume = 60
	}
	o.Volume = clampInt(o.Volume, 1, 100)
	o.Cooldown = clampInt(o.Cooldown, 0, 60)
}
