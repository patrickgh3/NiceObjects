package main

import (
    "fmt"
    "os"
    "os/signal"
    "bufio"
    "path/filepath"
    "strings"
    "github.com/fsnotify/fsnotify"
    "time"
    "syscall"
    "github.com/sqweek/dialog"
)

var humanDir     string
var projectPath  string
var projectDir   string
var gmObjectsDir string
var gmScriptsDir string

// "Reverb" refers to translations causing file Write events back and forth
// between the GM and human folders.
const reverbSpacing time.Duration = 1 * time.Second
const dedupSpacing  time.Duration = 100 * time.Millisecond
var gmChanged       time.Time
var humanChanged    time.Time
var lastGMFileChanged    string
var lastHumanFileChanged string

const usage string = `Usage:
gm_txt.exe [project_file]
If you provide no arguments, it'll open a file picker dialog.
`

func main () {
    InitTranslations()

    // Parse arguments

    // No args opens windows file picker for project path
    if len(os.Args) < 2 {
        var err error
        projectPath, err = dialog.File().
                Title("Select GMS project file").
                SetStartDir(".").
                Filter("GMS project file (*.project.gmx)", "project.gmx").
                Load()
        if err != nil {
            fmt.Printf("Error in project file select dialog: %v\n", err)
            return
        }

    } else if len(os.Args) == 2 && os.Args[1] == "--help" {
        fmt.Println(usage)
        return

    // One arg (non-help) specifies project path
    } else if len(os.Args) == 2 {
        projectPath = os.Args[1]

    } else {
        fmt.Println(usage)
        return
    }

    // Verify project path is okay.

    fi, err := os.Stat(projectPath)
    if err != nil {
        fmt.Printf("Error Stat-ing project file %v : %v\n",
            projectPath, err)
        return
    }
    if os.IsNotExist(err) {
        fmt.Printf("Project file %v does not exist\n", projectPath)
        return
    }
    if fi.IsDir() {
        fmt.Printf("Must select project file, not directory\n")
        return
    }

    // Compute relative paths.

    projectDir, _ = filepath.Split(projectPath)
    gmObjectsDir = filepath.Join(projectDir, "objects")
    gmScriptsDir = filepath.Join(projectDir, "scripts")
    humanDir     = filepath.Join(projectDir, "gm_txt")

    // Start listening for SIGINT (Ctrl-C)

    sigchan := make(chan os.Signal, 2)
    signal.Notify(sigchan, os.Interrupt, syscall.SIGTERM)

    // Create human folder

    err = os.MkdirAll(humanDir, os.ModePerm)
    if err != nil {
        fmt.Printf("Error creating gm_txt directory &v: %v\n", humanDir, err)
        return
    }

    // Translate all GM objects.

    // implements filepath.WalkFunc
    f := func (path string, info os.FileInfo, err error) error {
        // Skip directories and extraneous files.
        if info.IsDir() || !strings.HasSuffix(path, ".object.gmx") {
            return nil
        }

        // Compute translated file path.
        resourceName := strings.TrimSuffix(filepath.Base(path), ".object.gmx")
        destPath := filepath.Join(humanDir, resourceName+".gmo")

        // Translate.
        err = GMObjectFileToHumanObjectFile(path, destPath)
        if err != nil {
            fmt.Printf("Error initially translating %v: %v\n",
                    resourceName, err)
            return err
        }
        return err
    }

    err = filepath.Walk(gmObjectsDir, f)
    if err != nil {
        fmt.Printf("Error during initial translation of all GM objects "+
                "to human objects: %v\n", err)
        return
    }

    // Copy over all GM scripts.

    // implements filepath.WalkFunc
    f = func (path string, info os.FileInfo, err error) error {
        // Skip directories and extraneous files.
        if info.IsDir() || filepath.Ext(path) != ".gml" {
            return nil
        }

        destPath := filepath.Join(humanDir, filepath.Base(path))
        return cp(destPath, path)
    }

    err = filepath.Walk(gmScriptsDir, f)
    if err != nil {
        fmt.Printf("Error during initial copying of scripts: %v\n", err)
        return
    }

    // Start monitoring files for changes

    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        fmt.Printf("Error creating fsnotify watcher: %v\n", err)
        return
    }
    defer watcher.Close()

    // Watcher must be in a separate goroutine
    go func () {
        for {
            select {
            case event := <-watcher.Events:
                processWatcherEvent(event)
            case err := <-watcher.Errors:
                fmt.Printf("Fsnotify watcher error: %v\n", err)
            }
        }
    }()

    if err := watcher.Add(humanDir); err != nil {
        fmt.Printf("Error assigning human dir to fsnotify watcher: %v\n", err)
    }
    if err := watcher.Add(gmObjectsDir); err != nil {
        fmt.Printf("Error assigning GM objects dir to fsnotify watcher: %v\n",
                err)
    }
    if err := watcher.Add(gmScriptsDir); err != nil {
        fmt.Printf("Error assigning GM scripts dir to fsnotify watcher: %v\n",
                err)
    }

    // Listen for typed commands on Stdin

    go func () {
        scan := bufio.NewScanner(os.Stdin)
        for scan.Scan() {
            text := scan.Text()
            if text == "help" {
                fmt.Println(helpMessage)
            } else if text == "objects" {
                fmt.Println(objectsHelpMessage)
            } else if text == "events" {
                fmt.Println(eventsHelpMessage())
            }
        }
    }()

    // Surrender this goroutine to the watcher, and wait for control signal

    fmt.Println("Listening (Ctrl-C to quit) (type \"help\" for help)")

    <-sigchan

    // Upon control signal, remove created directory

    err = os.RemoveAll(humanDir)
    if err != nil {
        fmt.Printf("Error removing gm_txt directory %v: %v\n", humanDir, err)
        return
    }
    fmt.Println("Success")
}

func processWatcherEvent (event fsnotify.Event) {
    if event.Op == fsnotify.Write {
        ext := filepath.Ext(event.Name)
        isHuman := strings.HasPrefix(event.Name, humanDir)
        isHumanObj := isHuman && ext == ".gmo"
        isHumanScript := isHuman && ext == ".gml"
        isGMObj := strings.HasPrefix(event.Name, gmObjectsDir) &&
                ext == ".gmx" // close enough to ".object.gmx"
        isGMScript := strings.HasPrefix(event.Name, gmScriptsDir) &&
                ext == ".gml"

        if isHumanObj {
            if humanFileTimingOk(event.Name) {
                humanChanged = time.Now()
                lastHumanFileChanged = event.Name
                translateHumanObject(event.Name)
            }
        } else if isHumanScript {
            if humanFileTimingOk(event.Name) {
                humanChanged = time.Now()
                lastHumanFileChanged = event.Name
                copyHumanScript(event.Name)
            }
        } else if isGMObj {
            if gmFileTimingOk(event.Name) {
                gmChanged = time.Now()
                lastGMFileChanged = event.Name
                translateGMObject(event.Name)
            }
        } else if isGMScript {
            if gmFileTimingOk(event.Name) {
                gmChanged = time.Now()
                lastGMFileChanged = event.Name
                copyGMScript(event.Name)
            }
        }
    }
}

func humanFileTimingOk (humanFile string) bool {
    return time.Since(gmChanged) > reverbSpacing &&
            (lastHumanFileChanged != humanFile ||
            time.Since(humanChanged) > dedupSpacing)
}

func gmFileTimingOk (gmFile string) bool {
    return time.Since(humanChanged) > reverbSpacing &&
            (lastGMFileChanged != gmFile ||
            time.Since(gmChanged) > dedupSpacing)
}

func copyHumanScript (humanScriptPath string) {
    fn := filepath.Base(humanScriptPath)
    gmScriptPath := filepath.Join(gmScriptsDir, fn)
    scriptName := strings.Split(fn, ".")[0]

    // GM file not existing before translation meanse we have to add it to the
    // project file

    _, err := os.Stat(gmScriptPath)
    gmFileExisted := !os.IsNotExist(err) 

    // Copy script

    err = cp(gmScriptPath, humanScriptPath)
    if err != nil {
        fmt.Printf("[%v] %v\n", time.Now().Format("15:04:05"), err)
    } else {
        fmt.Printf("[%v] Copied %v\n",
                time.Now().Format("15:04:05"), scriptName)
    }

    // If necessary, add to project file

    if !gmFileExisted {
        err = AppendResourceToGMProject(fn, "script", "scripts")
        if err != nil {
            fmt.Printf("[%v] %v\n", time.Now().Format("15:04:05"), err)
        } else {
            fmt.Printf("[%v] Project file updated %v\n",
                    time.Now().Format("15:04:05"), scriptName)
        }
    }
}

func copyGMScript (gmScriptPath string) {
    fn := filepath.Base(gmScriptPath)
    humanScriptPath := filepath.Join(humanDir, fn)
    err := cp(humanScriptPath, gmScriptPath)
    if err != nil {
        fmt.Printf("[%v] (From GM) %v\n", time.Now().Format("15:04:05"), err)
    } else {
        scriptName := strings.Split(fn, ".")[0]
        fmt.Printf("[%v] (From GM) Copied %v\n",
                time.Now().Format("15:04:05"), scriptName)
    }
}

func translateHumanObject (humanObjPath string) {
    objName := strings.Split(filepath.Base(humanObjPath), ".")[0]
    gmObjPath := filepath.Join(gmObjectsDir, objName + ".object.gmx")

    // GM file not existing before translation meanse we have to add it to the
    // project file

    _, err := os.Stat(gmObjPath)
    gmObjFileExisted := !os.IsNotExist(err) 

    // Translate object

    err = HumanObjectFileToGMObjectFile(humanObjPath, gmObjPath)
    if err != nil {
        fmt.Printf("[%v] %v\n", time.Now().Format("15:04:05"), err)
    } else {
        fmt.Printf("[%v] Translated %v\n", time.Now().Format("15:04:05"),
                objName)
        // Touching the project file causes GM:Studio to close all
        // folders, which is annoying.
        //touchProjectFile()
    }

    // If necessary, add to project file

    if !gmObjFileExisted {
        err = AppendResourceToGMProject(objName, "object", "objects")
        if err != nil {
            fmt.Printf("[%v] %v\n", time.Now().Format("15:04:05"), err)
        } else {
            fmt.Printf("[%v] Project file updated %v\n",
                    time.Now().Format("15:04:05"), objName)
        }
    }
}

func translateGMObject (gmObjPath string) {
    objName := strings.Split(filepath.Base(gmObjPath), ".")[0]
    humanObjPath := filepath.Join(humanDir, objName + ".gmo")

    err := GMObjectFileToHumanObjectFile(gmObjPath, humanObjPath)
    if err != nil {
        fmt.Printf("[%v] (From GM) %v\n", time.Now().Format("15:04:05"), err)
    } else {
        fmt.Printf("[%v] (From GM) Translated %v\n",
                time.Now().Format("15:04:05"), objName)
    }
}

