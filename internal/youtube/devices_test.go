package youtube

import "testing"

func TestParseDevicesShowsWindowsNames(t *testing.T) {
	out := "List of detected audio devices:\r\n" +
		"  'auto' (Autoselect device)\r\n" +
		"  'wasapi/{1a7e}' (Mi Monitor (NVIDIA High Definition Audio))\r\n" +
		"  'wasapi/{b069}' (Динамики (2- fifine Microphone))\r\n" +
		"  'openal' (Default (openal))\r\n"
	got := parseDevices(out)
	if len(got) != 2 {
		t.Fatalf("ждал два устройства без auto и openal, вышло %v", got)
	}
	if got[0].ID != "wasapi/{1a7e}" || got[0].Name != "Mi Monitor (NVIDIA High Definition Audio)" {
		t.Errorf("первое: %+v", got[0])
	}
	if got[1].Name != "Динамики (2- fifine Microphone)" {
		t.Errorf("второе: %+v", got[1])
	}
}
