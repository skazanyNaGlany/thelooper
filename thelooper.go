package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/emersion/go-autostart"
	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/gonutz/w32"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const AppName = "TheLooper"
const AppVersion = "0.1"
const Mp3FilePathname = "TEST_One minute of silence (ID 0917)_BSB.mp3"
const LockFilename = AppName + ".pid"
const Mp3LoopsCount = -1
const PROCESS_ALL_ACCESS = 0x1F0FFF

// const Mp3FilePathname = "sample-3s.mp3"
// const Mp3FilePathname = "E:\\Music\\Olof Gustafsson - Motorhead Soundtrack.mp3"
// const Mp3FilePathname = "1-hour-of-silence.mp3"

type MP3AudioFile struct {
	Streamer      beep.StreamSeekCloser
	Format        beep.Format
	Control       beep.Ctrl
	OpenFile      *os.File
	IsDonePlaying bool
}

func playMp3File(mp3FilePathname string, count int) (*MP3AudioFile, error) {
	var err error
	mp3AudioFile := MP3AudioFile{}

	mp3AudioFile.OpenFile, err = os.Open(mp3FilePathname)

	if err != nil {
		return nil, err
	}

	mp3AudioFile.Streamer, mp3AudioFile.Format, err = mp3.Decode(mp3AudioFile.OpenFile)

	if err != nil {
		return nil, err
	}

	speaker.Init(mp3AudioFile.Format.SampleRate, mp3AudioFile.Format.SampleRate.N(time.Second/10))

	looper := beep.Loop(count, mp3AudioFile.Streamer)
	sequencer := beep.Seq(looper, beep.Callback(func() {
		mp3AudioFile.IsDonePlaying = true
	}))
	mp3AudioFile.Control = beep.Ctrl{Streamer: sequencer}

	speaker.Play(&mp3AudioFile.Control)

	return &mp3AudioFile, err
}

func getWindowsVersion() (map[string]interface{}, error) {
	var err error
	var result = make(map[string]interface{})

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()

	result["CurrentVersion"], _, err = k.GetStringValue("CurrentVersion")
	if err != nil {
		return nil, err
	}

	result["ProductName"], _, err = k.GetStringValue("ProductName")
	if err != nil {
		return nil, err
	}

	result["CurrentMajorVersionNumber"], _, err = k.GetIntegerValue("CurrentMajorVersionNumber")
	if err != nil {
		return nil, err
	}

	result["CurrentMinorVersionNumber"], _, err = k.GetIntegerValue("CurrentMinorVersionNumber")
	if err != nil {
		return nil, err
	}

	result["CurrentBuild"], _, err = k.GetStringValue("CurrentBuild")
	if err != nil {
		return nil, err
	}

	return result, nil
}

func checkWindowsVersion() {
	if runtime.GOOS != "windows" {
		log.Fatal("This program can be used only on Windows.")
	}

	windowsVersion, err := getWindowsVersion()

	if err != nil {
		log.Fatal(err)
	}

	if windowsVersion["CurrentMajorVersionNumber"].(uint64) < 7 {
		log.Fatal("This program must be used on Windows 7 or grater.")
	}
}

func getFullAppName() string {
	return fmt.Sprintf("%v v%v", AppName, AppVersion)
}

func printAppName() {
	log.Println(
		getFullAppName())
	log.Println()
}

func printUsages() {
	log.Printf("Usage: %v <option>", os.Args[0])

	log.Println()
	log.Println("Options:")

	log.Println("\t--install")
	log.Println("\t\t autorun with the system")
	log.Println("\t--uninstall")
	log.Println("\t\t do not autorun with the system")
	log.Println("\t--start")
	log.Println("\t\t start looping blank MP3")
	log.Println("\t--stop")
	log.Println("\t\t stop looping blank MP3")
	log.Println("\t--status")
	log.Println("\t\t check if app is running (playing) and installed")
}

func playLoopedMp3() {
	var err error
	var mp3AudioFile *MP3AudioFile

	log.Printf("Playing %v", Mp3FilePathname)

	mp3AudioFile, err = playMp3File(Mp3FilePathname, Mp3LoopsCount)

	if err != nil {
		log.Fatal(err)
	}

	defer mp3AudioFile.OpenFile.Close()
	defer mp3AudioFile.Streamer.Close()

	for !mp3AudioFile.IsDonePlaying {
		time.Sleep(time.Second * 1)
	}
}

func fileExists(name string) (bool, error) {
	_, err := os.Stat(name)

	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
}

func getRunningPid() (uint32, error) {
	tempFilePathname := getPidFilePathname()

	exists, err := fileExists(tempFilePathname)

	if err != nil {
		return 0, err
	}

	if !exists {
		return 0, os.ErrNotExist
	}

	content, err := ioutil.ReadFile(tempFilePathname)

	if err != nil {
		return 0, err
	}

	pidStr := strings.TrimSpace(string(content))
	pid, err := strconv.ParseInt(pidStr, 10, 32)

	if err != nil {
		return 0, err
	}

	return uint32(pid), nil
}

func getPidFilePathname() string {
	return filepath.Join(
		os.TempDir(),
		LockFilename)
}

func lock() error {
	pid, err := getRunningPid()

	if err == nil {
		// seems to be running
		if isProcessRunning(pid) {
			return os.ErrExist
		}
	}

	tempFilePathname := getPidFilePathname()

	f, err := os.Create(tempFilePathname)

	if err != nil {
		return err
	}

	pidStr := strconv.Itoa(os.Getpid())

	f.WriteString(pidStr)
	f.Close()

	return nil
}

func markRunning() {
	err := lock()

	if err != nil {
		log.Printf("Cannot create lock file, perhaps %v is still running: %v", AppName, err)
		log.Fatalf("If %v is not running try to delete lock file %v", AppName, getPidFilePathname())
	}
}

func unlock() error {
	tempFilePathname := getPidFilePathname()

	exists, err := fileExists(tempFilePathname)

	if err != nil {
		return err
	}

	if !exists {
		return os.ErrNotExist
	}

	err = os.Remove(tempFilePathname)

	if err != nil {
		return err
	}

	return nil
}

func markNotRunning(quiet bool) {
	err := unlock()

	if err != nil {
		log.Fatalf("Cannot remove lock file: %v", err)
	}
}

func listenForExit() {
	signalChan := make(chan os.Signal, 1)

	signal.Notify(
		signalChan,
		os.Interrupt)

	go func() {
		s := <-signalChan

		log.Printf("Got signal: %v", s)

		markNotRunning(true)

		os.Exit(1)
	}()
}

func cleanupOnExit() {
	listenForExit()
}

func shouldPrintUsages() bool {
	len_args := len(os.Args)

	return len_args != 2 || (len_args > 1 && os.Args[1] == "--help")
}

func checkIfRunning() {
	pid, err := getRunningPid()

	if err != nil {
		log.Printf("%v is not running.", AppName)

		return
	}

	if !isProcessRunning(pid) {
		log.Printf("%v is not running (no process found).", AppName)

		return
	}

	log.Printf("%v is running, PID: %v", AppName, pid)
}

func checkInstalled() {
	app, err := getGoAutostartApp()

	if err != nil {
		log.Fatal(err)
	}

	if app.IsEnabled() {
		log.Fatal("App is installed.")
	} else {
		log.Fatal("App is not installed.")
	}
}

func printAppStatus() {
	checkIfRunning()
	checkInstalled()
}

func getOpenProcessExecutable(handle syscall.Handle) (string, error) {
	buf := make([]uint16, 1024*64)

	winHandle := windows.Handle(handle)
	nilHandle := windows.Handle(0)

	err := windows.GetModuleFileNameEx(winHandle, nilHandle, &buf[0], 1024*64)

	if err != nil {
		return "", err
	}

	return syscall.UTF16ToString(buf), nil
}

func isProcessRunning(pid uint32) bool {
	processHandle, err := syscall.OpenProcess(PROCESS_ALL_ACCESS, false, pid)

	if err != nil {
		return false
	}

	processExecutable, _ := getOpenProcessExecutable(processHandle)

	syscall.CloseHandle(processHandle)

	currentExecutable, err := os.Executable()

	if err != nil {
		log.Fatal(err)
	}

	return processExecutable == currentExecutable
}

func stopRunning() {
	pid, err := getRunningPid()

	if err != nil {
		log.Printf("%v is not running.", AppName)

		return
	}

	if !isProcessRunning(pid) {
		log.Fatal(os.ErrNotExist)
	}

	processHandle, err := syscall.OpenProcess(PROCESS_ALL_ACCESS, false, pid)

	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		err := syscall.CloseHandle(processHandle)

		if err != nil {
			log.Fatal(err)
		}
	}()

	err = syscall.TerminateProcess(processHandle, 1)

	if err != nil {
		log.Fatal(err)
	}

	syscall.WaitForSingleObject(processHandle, syscall.INFINITE)

	log.Println("App stopped.")
}

func hideConsole() {
	console := w32.GetConsoleWindow()
	if console == 0 {
		return // no console attached
	}
	// If this application is the process that created the console window, then
	// this program was not compiled with the -H=windowsgui flag and on start-up
	// it created a console along with the main application window. In this case
	// hide the console window.
	// See
	// http://stackoverflow.com/questions/9009333/how-to-check-if-the-program-is-run-from-a-console
	_, consoleProcID := w32.GetWindowThreadProcessId(console)
	if w32.GetCurrentProcessId() == consoleProcID {
		w32.ShowWindowAsync(console, w32.SW_HIDE)
	}
}

func getGoAutostartApp() (*autostart.App, error) {
	executable, err := os.Executable()

	if err != nil {
		return nil, err
	}

	fullAppName := getFullAppName()
	app := autostart.App{
		Name:        fullAppName,
		DisplayName: fullAppName,
		Exec:        []string{executable, "--start"},
	}

	return &app, nil
}

func installAutorun() {
	app, err := getGoAutostartApp()

	if err != nil {
		log.Fatal(err)
	}

	if app.IsEnabled() {
		log.Fatal("App already installed.")
	}

	err = app.Enable()

	if err != nil {
		log.Fatal(err)
	}

	log.Println("App installed.")
}

func uninstallAutorun() {
	app, err := getGoAutostartApp()

	if err != nil {
		log.Fatal(err)
	}

	if !app.IsEnabled() {
		log.Fatal("App is not installed.")
	}

	err = app.Disable()

	if err != nil {
		log.Fatal(err)
	}

	log.Println("App uninstalled.")
}

func changeCurrentWorkingDir() {
	exeDir := filepath.Dir(os.Args[0])
	os.Chdir(exeDir)
}

func main() {
	printAppName()
	checkWindowsVersion()
	changeCurrentWorkingDir()

	if shouldPrintUsages() {
		printUsages()

		os.Exit(1)
	}

	if os.Args[1] == "--start" {
		markRunning()
		defer markNotRunning(false)
		cleanupOnExit()

		hideConsole()
		playLoopedMp3()
	} else if os.Args[1] == "--status" {
		printAppStatus()
	} else if os.Args[1] == "--stop" {
		stopRunning()
	} else if os.Args[1] == "--install" {
		installAutorun()
	} else if os.Args[1] == "--uninstall" {
		uninstallAutorun()
	} else {
		printUsages()
	}
}
