// Teams Status Scheduler: sets your Microsoft Teams status from schedules
// (by operating the Teams app/web UI, so no Entra ID app registration is
// needed) and can keep the computer awake.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"teamsstatus/internal/config"
	"teamsstatus/internal/engine"
	"teamsstatus/internal/keepawake"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

//go:embed assets/tray-template.png
var trayTemplateIcon []byte

//go:embed assets/tray.png
var trayIcon []byte

func setupLogging() {
	dir := config.Dir()
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "teams-status.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 1<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags)
}

func main() {
	hidden := flag.Bool("hidden", false, "start with the window hidden (used by launch at login)")
	flag.Parse()
	setupLogging()
	log.Printf("Teams Status Scheduler %s starting (%s/%s)", version, runtime.GOOS, runtime.GOARCH)

	store, err := config.Load(filepath.Join(config.Dir(), "config.json"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	router := engine.NewRouter(store)
	eng := engine.New(store, router, keepawake.New())

	var window *application.WebviewWindow
	showWindow := func() {
		application.InvokeAsync(func() {
			window.Show()
			window.UnMinimise()
			window.Focus()
		})
	}

	app := application.New(application.Options{
		Name:        "Teams Status Scheduler",
		Description: "Sets your Microsoft Teams status from a schedule",
		Assets:      application.AssetOptions{Handler: newServer(store, eng, router), DisableLogging: true},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyRegular,
		},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "com.teamsstatusscheduler.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { showWindow() },
		},
		OnShutdown: eng.Shutdown,
	})

	if runtime.GOOS == "darwin" {
		menu := app.NewMenu()
		menu.AddRole(application.AppMenu)
		menu.AddRole(application.EditMenu) // copy/paste in text fields
		menu.AddRole(application.WindowMenu)
		app.Menu.SetApplicationMenu(menu)
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { showWindow() })
	}

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "Teams Status Scheduler",
		Width:     1000,
		Height:    820,
		MinWidth:  720,
		MinHeight: 560,
		URL:       "/",
		Hidden:    *hidden || store.Get().Settings.StartHidden,
	})
	// Closing the window keeps the app running in the tray / menu bar.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	buildTray(app, store, eng, showWindow)
	// The engine updates the tray, so it may only start once the app's main loop runs.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { eng.Start() })

	// Ctrl-C / kill / system shutdown: quit cleanly so Teams is reset and sleep released.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; application.InvokeAsync(app.Quit) }()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func buildTray(app *application.App, store *config.Store, eng *engine.Engine, showWindow func()) {
	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("Teams Status Scheduler")

	menu := app.NewMenu()
	statusItem := menu.Add("Starting…").SetEnabled(false)
	menu.AddSeparator()
	menu.Add("Open Teams Status Scheduler").OnClick(func(*application.Context) { showWindow() })
	pauseItem := menu.Add("Pause schedules")
	pauseItem.OnClick(func(*application.Context) { _ = eng.SetPaused(!eng.Snapshot().Paused) })
	menu.Add("Apply now").OnClick(func(*application.Context) { eng.ForceApply() })
	awake := menu.AddSubmenu("Prevent sleep")
	modes := []struct{ mode, label string }{
		{config.AwakeOff, "Off"},
		{config.AwakeScheduled, "While a schedule is active"},
		{config.AwakeAlways, "Always, while the app is running"},
	}
	var awakeItems []*application.MenuItem
	for _, m := range modes {
		m := m
		awakeItems = append(awakeItems, awake.AddRadio(m.label, false).OnClick(func(*application.Context) { _ = eng.SetKeepAwake(m.mode) }))
	}
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	if runtime.GOOS != "darwin" {
		tray.OnClick(showWindow) // Windows: left-click opens the window; right-click shows the menu
	}

	var last string
	eng.OnChange = func(st engine.Status) {
		summary := "No schedule active — Teams automatic"
		if st.Current != nil {
			summary = fmt.Sprintf("%s — %s", st.Current.Label, st.Current.Schedule)
		}
		if st.Paused {
			summary = "Paused"
		}
		if !st.Ready {
			summary = "⚠ " + st.Info
		} else if st.LastError != "" {
			summary = "⚠ " + summary
		}
		key := fmt.Sprintf("%s|%v", summary, st.Paused)
		application.InvokeAsync(func() {
			if key != last {
				statusItem.SetLabel(summary)
				if st.Paused {
					pauseItem.SetLabel("Resume schedules")
				} else {
					pauseItem.SetLabel("Pause schedules")
				}
				tray.SetTooltip("Teams Status Scheduler — " + summary)
				last = key
			}
			current := store.Get().Settings.KeepAwake
			for i, m := range modes {
				awakeItems[i].SetChecked(m.mode == current)
			}
			menu.Update()
		})
	}
}
