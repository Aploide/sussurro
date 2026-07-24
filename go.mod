module github.com/cesp99/sussurro

go 1.25.0

require (
	github.com/AshkanYarmoradi/go-llama.cpp v0.0.0-20240314183750-6a8041ef6b46
	github.com/atotto/clipboard v0.1.4
	github.com/gen2brain/malgo v0.11.24
	github.com/getlantern/systray v1.2.2
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0-20260209103306-764482c3175d
	github.com/micmonay/keybd_event v1.1.2
	github.com/spf13/viper v1.21.0
	github.com/webview/webview_go v0.0.0-20240831120633-6173450d4dd6
	golang.design/x/hotkey v0.4.1
	golang.org/x/sys v0.47.0
)

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/getlantern/context v0.0.0-20190109183933-c447772a6520 // indirect
	github.com/getlantern/errors v0.0.0-20190325191628-abdb3e3e36f7 // indirect
	github.com/getlantern/golog v0.0.0-20190830074920-4ef2e798c2d7 // indirect
	github.com/getlantern/hex v0.0.0-20190417191902-c6586a6fe0b7 // indirect
	github.com/getlantern/hidden v0.0.0-20190325191715-f02dbb02be55 // indirect
	github.com/getlantern/ops v0.0.0-20190325191751-d70cb0d6f85f // indirect
	github.com/go-stack/stack v1.8.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/oxtoacart/bpool v0.0.0-20190530202638-03653db5a59c // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.design/x/mainthread v0.3.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
)

replace github.com/ggerganov/whisper.cpp/bindings/go => ./third_party/whisper.cpp/bindings/go

replace github.com/AshkanYarmoradi/go-llama.cpp => ./third_party/go-llama.cpp
